package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockEcsClient implements EcsAPIClient for tests. The embedded interface is
// left nil, so calling any method not overridden here panics.
type mockEcsClient struct {
	mock.Mock
	EcsAPIClient
}

func (m *mockEcsClient) DescribeClusters(ctx context.Context, in *ecs.DescribeClustersInput, _ ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.DescribeClustersOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) ListClusters(ctx context.Context, in *ecs.ListClustersInput, _ ...func(*ecs.Options)) (*ecs.ListClustersOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.ListClustersOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) ListTasks(ctx context.Context, in *ecs.ListTasksInput, _ ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.ListTasksOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.DescribeTasksOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) DescribeTaskDefinition(ctx context.Context, in *ecs.DescribeTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.DescribeTaskDefinitionOutput)
	return out, args.Error(1)
}

// newTaskDefinition builds a task definition identified by its ARN.
// Container definitions are optional and default to none.
func newTaskDefinition(taskDefinitionArn string, family string, revision int32, containerDefinitions ...types.ContainerDefinition) types.TaskDefinition {
	return types.TaskDefinition{
		TaskDefinitionArn:    aws.String(taskDefinitionArn),
		Family:               aws.String(family),
		Revision:             revision,
		ContainerDefinitions: containerDefinitions,
	}
}

// newRunningTask builds a task with a single container exposing port over TCP.
func newRunningTask(clusterArn string, lastStatus string, taskArn string, taskDefinitionArn string, containerName string, port int32) types.Task {
	return types.Task{
		TaskArn:           aws.String(taskArn),
		TaskDefinitionArn: aws.String(taskDefinitionArn),
		ClusterArn:        aws.String(clusterArn),
		LastStatus:        aws.String(lastStatus),
		Containers: []types.Container{
			{
				Name:       aws.String(containerName),
				LastStatus: aws.String("RUNNING"),
				NetworkBindings: []types.NetworkBinding{
					{ContainerPort: aws.Int32(port), HostPort: aws.Int32(port), Protocol: types.TransportProtocolTcp},
				},
			},
		},
	}
}

func TestGetClusterARNs_WithClusterIds(t *testing.T) {
	client := &mockEcsClient{}
	client.On("DescribeClusters", mock.Anything, &ecs.DescribeClustersInput{
		Clusters: []string{"foo", "bar"},
	}).Return(&ecs.DescribeClustersOutput{
		Clusters: []types.Cluster{
			{ClusterArn: aws.String("arn:aws:ecs:eu-central-1:123456789012:cluster/foo")},
			{ClusterArn: aws.String("arn:aws:ecs:eu-central-1:123456789012:cluster/bar")},
		},
	}, nil).Once()

	explorer := &EcsTaskExplorer{
		ecs:        client,
		clusterIds: []string{"foo", "bar"},
	}

	got, err := explorer.GetClusterARNs(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{
		"arn:aws:ecs:eu-central-1:123456789012:cluster/foo",
		"arn:aws:ecs:eu-central-1:123456789012:cluster/bar",
	}, got)
	client.AssertExpectations(t)
}

func TestGetClusterARNs_WithClusterIds_DescribeClustersError(t *testing.T) {
	apiErr := errors.New("describe clusters failed")
	
	client := &mockEcsClient{}
	client.On("DescribeClusters", mock.Anything, &ecs.DescribeClustersInput{
		Clusters: []string{"foo", "bar"},
	}).Return(nil, apiErr).Once()

	explorer := &EcsTaskExplorer{
		ecs:        client,
		clusterIds: []string{"foo", "bar"},
	}

	got, err := explorer.GetClusterARNs(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got)
	client.AssertExpectations(t)
}

func TestGetClusterARNs_WithClusterIds_DescribeClusters_Failures(t *testing.T) {
	missingArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/does_not_exist"
	missingReason := "Cluster does not exist"

	client := &mockEcsClient{}
	client.On("DescribeClusters", mock.Anything, &ecs.DescribeClustersInput{
		Clusters: []string{"foo", "does_not_exist"},
	}).Return(&ecs.DescribeClustersOutput{
		Clusters: []types.Cluster{
			{ClusterArn: aws.String("arn:aws:ecs:eu-central-1:123456789012:cluster/foo")},
		},
		Failures: []types.Failure{
			{
				Arn:    &missingArn,
				Reason: &missingReason,
			},
		},
	}, nil).Once()

	explorer := &EcsTaskExplorer{
		ecs:        client,
		clusterIds: []string{"foo", "does_not_exist"},
	}

	got, err := explorer.GetClusterARNs(context.Background())

	assert.EqualError(t, err, fmt.Sprintf("failed to describe 1 cluster(s):\n- %s: %s", missingArn, missingReason))
	assert.Nil(t, got)
	client.AssertExpectations(t)
}

func TestGetClusterARNs_WithoutClusterIds(t *testing.T) {
	client := &mockEcsClient{}
	
	client.On("ListClusters", mock.Anything, mock.MatchedBy(func(in *ecs.ListClustersInput) bool {
		return in.NextToken == nil
	})).Return(&ecs.ListClustersOutput{
		ClusterArns: []string{"arn:aws:ecs:eu-central-1:123456789012:cluster/foo"},
		NextToken:   aws.String("page-2"),
	}, nil).Once()

	client.On("ListClusters", mock.Anything, mock.MatchedBy(func(in *ecs.ListClustersInput) bool {
		return aws.ToString(in.NextToken) == "page-2"
	})).Return(&ecs.ListClustersOutput{
		ClusterArns: []string{"arn:aws:ecs:eu-central-1:123456789012:cluster/bar"},
	}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.GetClusterARNs(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{
		"arn:aws:ecs:eu-central-1:123456789012:cluster/foo",
		"arn:aws:ecs:eu-central-1:123456789012:cluster/bar",
	}, got)
	client.AssertExpectations(t)
}

func TestGetDetailedTaskDataInClusters_ListTasks_ErrorOnLaterPage(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	apiErr := errors.New("list tasks failed")

	client := &mockEcsClient{}

	// page 1 succeeds
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}).Return(&ecs.ListTasksOutput{
		TaskArns:  []string{task1Arn},
		NextToken: aws.String("page-2"),
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task1Arn},
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: []types.Task{{TaskArn: &task1Arn}},
	}, nil).Once()

	// page 2 fails on ListTasks
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster:   &clusterArn,
		NextToken: aws.String("page-2"),
	}).Return(nil, apiErr).Once()

	explorer := &EcsTaskExplorer{ecs: client}

	// fails before any task definition is described, so no DescribeTaskDefinition expectations
	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{clusterArn})

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got, "no partial results should be returned alongside an error")
	client.AssertExpectations(t)
}

func TestGetDetailedTaskDataInClusters_DescribeTasks_ErrorOnLaterPage(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	apiErr := errors.New("describe tasks failed")

	client := &mockEcsClient{}

	// page 1 succeeds
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}).Return(&ecs.ListTasksOutput{
		TaskArns:  []string{task1Arn},
		NextToken: aws.String("page-2"),
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task1Arn},
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: []types.Task{{TaskArn: &task1Arn}},
	}, nil).Once()

	// page 2 lists ok but fails on DescribeTasks
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster:   &clusterArn,
		NextToken: aws.String("page-2"),
	}).Return(&ecs.ListTasksOutput{
		TaskArns: []string{task2Arn},
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task2Arn},
	}).Return(nil, apiErr).Once()

	explorer := &EcsTaskExplorer{ecs: client}

	// fails before any task definition is described, so no DescribeTaskDefinition expectations
	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{clusterArn})

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got, "no partial results should be returned alongside an error")
	client.AssertExpectations(t)
}

func TestGetDetailedTaskDataInClusters_SingleCluster_MultiplePages(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	task3Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/3"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1)

	// it returns tasks regardless of state
	// refer to https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-lifecycle-explanation.html
	task1 := newRunningTask(clusterArn, "RUNNING", task1Arn, taskDefArn, "api", 8080)
	task2 := newRunningTask(clusterArn, "PENDING", task2Arn, taskDefArn, "worker", 9100)
	task3 := newRunningTask(clusterArn, "STOPPED", task3Arn, taskDefArn, "api", 8080)

	client := &mockEcsClient{}

	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}).Return(&ecs.ListTasksOutput{
		TaskArns:  []string{task1Arn, task2Arn},
		NextToken: aws.String("page-2"),
	}, nil).Once()
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster:   &clusterArn,
		NextToken: aws.String("page-2"),
	}).Return(&ecs.ListTasksOutput{
		TaskArns: []string{task3Arn},
	}, nil).Once()
	
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task1Arn, task2Arn},
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: []types.Task{task1, task2},
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task3Arn},
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: []types.Task{task3},
	}, nil).Once()

	// all three tasks share the definition, so it's described once
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &taskDef,
	}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{clusterArn})

	require.NoError(t, err)
	// single cluster, so page order is preserved
	assert.Equal(t, []DetailedTaskData{
		{task: task1, definition: taskDef},
		{task: task2, definition: taskDef},
		{task: task3, definition: taskDef},
	}, got)
	client.AssertExpectations(t)
}

func TestGetDetailedTaskDataInClusters_NoClusters(t *testing.T) {
	// no expectations: any call to the mock panics
	client := &mockEcsClient{}
	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{})

	require.NoError(t, err)
	assert.Empty(t, got)
	client.AssertExpectations(t)
}

func TestGetDetailedTaskDataInClusters_SingleCluster(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	apiTaskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"
	workerTaskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/worker:3"

	apiTaskDef := newTaskDefinition(apiTaskDefArn, "api", 1)
	workerTaskDef := newTaskDefinition(workerTaskDefArn, "worker", 3)

	task1 := newRunningTask(clusterArn, "RUNNING", task1Arn, apiTaskDefArn, "api", 8080)
	task2 := newRunningTask(clusterArn, "RUNNING", task2Arn, workerTaskDefArn, "worker", 9100)

	client := &mockEcsClient{}
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}).Return(&ecs.ListTasksOutput{
		TaskArns: []string{task1Arn, task2Arn},
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task1Arn, task2Arn},
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: []types.Task{task1, task2},
	}, nil).Once()

	// each task has its own definition
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(apiTaskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &apiTaskDef,
	}, nil).Once()
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(workerTaskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &workerTaskDef,
	}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{clusterArn})

	require.NoError(t, err)
	assert.ElementsMatch(t, []DetailedTaskData{
		{task: task1, definition: apiTaskDef},
		{task: task2, definition: workerTaskDef},
	}, got)
	client.AssertExpectations(t)
}

func TestGetDetailedTaskDataInClusters_TenClusters(t *testing.T) {
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"
	taskDef := newTaskDefinition(taskDefArn, "api", 1)

	client := &mockEcsClient{}

	// the same service runs in every cluster, so the shared definition is described once
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &taskDef,
	}, nil).Once()

	var clusterArns []string
	var want []DetailedTaskData
	for i := 0; i < 10; i++ {
		clusterArn := fmt.Sprintf("arn:aws:ecs:eu-central-1:123456789012:cluster/cluster-%d", i)
		taskArn := fmt.Sprintf("arn:aws:ecs:eu-central-1:123456789012:task/cluster-%d/1", i)
		task := newRunningTask(clusterArn, "RUNNING", taskArn, taskDefArn, "api", 8080)

		client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
			Cluster: &clusterArn,
		}).Return(&ecs.ListTasksOutput{
			TaskArns: []string{taskArn},
		}, nil).Once()
		client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
			Cluster: &clusterArn,
			Tasks:   []string{taskArn},
		}).Return(&ecs.DescribeTasksOutput{
			Tasks: []types.Task{task},
		}, nil).Once()

		clusterArns = append(clusterArns, clusterArn)
		want = append(want, DetailedTaskData{task: task, definition: taskDef})
	}

	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), clusterArns)

	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
	client.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestGetDetailedTaskDataInClusters_DescribeTasksFailuresAreLogged(t *testing.T) {
	var logs bytes.Buffer
	prevOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prevOutput)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	task3Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/3"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1)
	task1 := newRunningTask(clusterArn, "RUNNING", task1Arn, taskDefArn, "api", 8080)

	client := &mockEcsClient{}
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}).Return(&ecs.ListTasksOutput{
		TaskArns: []string{task1Arn, task2Arn, task3Arn},
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: &clusterArn,
		Tasks:   []string{task1Arn, task2Arn, task3Arn},
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: []types.Task{task1},
		Failures: []types.Failure{
			{Arn: aws.String(task2Arn), Reason: aws.String("MISSING")},
			{Arn: aws.String(task3Arn), Reason: aws.String("MISSING")},
		},
	}, nil).Once()

	// only the described task's definition is fetched
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &taskDef,
	}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{clusterArn})

	require.NoError(t, err)
	assert.Equal(t, []DetailedTaskData{{task: task1, definition: taskDef}}, got, "described tasks are still returned")
	assert.Contains(t, logs.String(), fmt.Sprintf("Failed to describe task %s in cluster %s: MISSING", task2Arn, clusterArn))
	assert.Contains(t, logs.String(), fmt.Sprintf("Failed to describe task %s in cluster %s: MISSING", task3Arn, clusterArn))
	client.AssertExpectations(t)
}

