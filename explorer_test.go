package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
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

func TestGetDetailedTaskDataInClusters_ClusterWithNoTasks(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"

	client := &mockEcsClient{}
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}).Return(&ecs.ListTasksOutput{
		TaskArns: []string{},
	}, nil).Once()
	// no DescribeTasks expectation: real ECS rejects an empty task list, so calling it panics the mock

	explorer := &EcsTaskExplorer{ecs: client}

	got, err := explorer.getDetailedTaskDataInClusters(context.Background(), []string{clusterArn})

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

// --- Discover ---
//
// Every Discover test mocks the same AWS call sequence (ListClusters, ListTasks, DescribeTasks,
// DescribeTaskDefinition); only the task/definition data differs, so the setup is shared.

// captureLogs redirects the global logger for the rest of the test, so callers must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	var logs bytes.Buffer
	prevOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prevOutput) })
	return &logs
}

// scrapeLabels returns docker labels that make a container a scrape target under testLabelConfig.
func scrapeLabels(port string, path string, scheme string) map[string]string {
	return map[string]string{
		testLabelConfig.FilterLabel: "true",
		testLabelConfig.PortLabel:   port,
		testLabelConfig.PathLabel:   path,
		testLabelConfig.SchemeLabel: scheme,
	}
}

func newContainerDefinition(name string, image string, dockerLabels map[string]string) types.ContainerDefinition {
	return types.ContainerDefinition{
		Name:         aws.String(name),
		Image:        aws.String(image),
		DockerLabels: dockerLabels,
	}
}

// newAwsvpcContainer builds a running container; an empty ip means no network interface attached yet.
func newAwsvpcContainer(name string, containerArn string, ip string) types.Container {
	container := types.Container{
		Name:         aws.String(name),
		ContainerArn: aws.String(containerArn),
		LastStatus:   aws.String("RUNNING"),
	}
	if ip != "" {
		container.NetworkInterfaces = []types.NetworkInterface{{PrivateIpv4Address: aws.String(ip)}}
	}
	return container
}

func newAwsvpcTask(clusterArn string, taskArn string, taskDefinitionArn string, group string, containers ...types.Container) types.Task {
	return types.Task{
		TaskArn:           aws.String(taskArn),
		TaskDefinitionArn: aws.String(taskDefinitionArn),
		ClusterArn:        aws.String(clusterArn),
		Group:             aws.String(group),
		LastStatus:        aws.String("RUNNING"),
		Containers:        containers,
	}
}

// expectClusters mocks ListClusters (no cluster IDs configured) returning a single page.
func expectClusters(client *mockEcsClient, clusterArns ...string) {
	client.On("ListClusters", mock.Anything, &ecs.ListClustersInput{}).Return(&ecs.ListClustersOutput{
		ClusterArns: clusterArns,
	}, nil).Once()
}

// expectTasksInCluster mocks a single page of ListTasks + DescribeTasks for the cluster.
func expectTasksInCluster(client *mockEcsClient, clusterArn string, tasks ...types.Task) {
	taskArns := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskArns = append(taskArns, aws.ToString(task.TaskArn))
	}
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: aws.String(clusterArn),
	}).Return(&ecs.ListTasksOutput{
		TaskArns: taskArns,
	}, nil).Once()
	client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
		Cluster: aws.String(clusterArn),
		Tasks:   taskArns,
	}).Return(&ecs.DescribeTasksOutput{
		Tasks: tasks,
	}, nil).Once()
}

func expectTaskDefinition(client *mockEcsClient, taskDefinition types.TaskDefinition) {
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: taskDefinition.TaskDefinitionArn,
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &taskDefinition,
	}, nil).Once()
}

// targetSummaries flattens discovered targets into readable one-liners, for order-independent comparison.
func targetSummaries(discovered []*DiscoveredTaskTargets) []string {
	summaries := make([]string, 0, len(discovered))
	for _, d := range discovered {
		summaries = append(summaries, fmt.Sprintf("%s %s %s %s %s",
			d.Labels.TaskArn, d.Labels.ContainerName, strings.Join(d.Targets, ","), d.Labels.MetricsPath, d.Labels.Scheme))
	}
	return summaries
}

func TestDiscover_SingleScrapableContainer(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	containerArn := "arn:aws:ecs:eu-central-1:123456789012:container/foo/1/api"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:3"

	taskDef := newTaskDefinition(taskDefArn, "api", 3,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", containerArn, "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// the only test checking every label, so the mapping is fully pinned down here
	assert.Equal(t, []*DiscoveredTaskTargets{
		{
			Targets: []string{"10.0.0.1:8080"},
			Labels: labels{
				TaskArn:       taskArn,
				TaskName:      "api",
				TaskRevision:  "3",
				TaskGroup:     "service:api",
				ClusterArn:    clusterArn,
				ContainerName: "api",
				ContainerArn:  containerArn,
				DockerImage:   "example/api:1.0",
				MetricsPath:   "/metrics",
				Scheme:        "http",
			},
		},
	}, got)
	client.AssertExpectations(t)
}

func TestDiscover_UnlabelledSidecarIsSkipped(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")),
		newContainerDefinition("envoy", "envoyproxy/envoy:v1.20", nil))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"),
		newAwsvpcContainer("envoy", taskArn+"/envoy", "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{taskArn + " api 10.0.0.1:8080 /metrics http"}, targetSummaries(got))
	client.AssertExpectations(t)
}

func TestDiscover_MultipleScrapableContainersInTask(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")),
		newContainerDefinition("worker", "example/worker:1.0", scrapeLabels("9100", "/prom", "https")))
	// awsvpc: containers in a task share the task's network interface
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"),
		newAwsvpcContainer("worker", taskArn+"/worker", "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// one entry per container, each with its own port, path and scheme
	assert.ElementsMatch(t, []string{
		taskArn + " api 10.0.0.1:8080 /metrics http",
		taskArn + " worker 10.0.0.1:9100 /prom https",
	}, targetSummaries(got))
	client.AssertExpectations(t)
}

func TestDiscover_TasksSharingDefinition(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")))
	task1 := newAwsvpcTask(clusterArn, task1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task1Arn+"/api", "10.0.0.1"))
	task2 := newAwsvpcTask(clusterArn, task2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task2Arn+"/api", "10.0.0.2"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task1, task2)
	expectTaskDefinition(client, taskDef) // .Once(): shared definition is only described once

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		task1Arn + " api 10.0.0.1:8080 /metrics http",
		task2Arn + " api 10.0.0.2:8080 /metrics http",
	}, targetSummaries(got))
	client.AssertExpectations(t)
}

func TestDiscover_MultipleClusters(t *testing.T) {
	fooClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	barClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/bar"
	fooTaskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	barTaskArn := "arn:aws:ecs:eu-central-1:123456789012:task/bar/1"
	apiTaskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"
	workerTaskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/worker:1"

	apiTaskDef := newTaskDefinition(apiTaskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")))
	workerTaskDef := newTaskDefinition(workerTaskDefArn, "worker", 1,
		newContainerDefinition("worker", "example/worker:1.0", scrapeLabels("9100", "/prom", "https")))
	fooTask := newAwsvpcTask(fooClusterArn, fooTaskArn, apiTaskDefArn, "service:api",
		newAwsvpcContainer("api", fooTaskArn+"/api", "10.0.0.1"))
	barTask := newAwsvpcTask(barClusterArn, barTaskArn, workerTaskDefArn, "service:worker",
		newAwsvpcContainer("worker", barTaskArn+"/worker", "10.0.1.1"))

	client := &mockEcsClient{}
	expectClusters(client, fooClusterArn, barClusterArn)
	expectTasksInCluster(client, fooClusterArn, fooTask)
	expectTasksInCluster(client, barClusterArn, barTask)
	expectTaskDefinition(client, apiTaskDef)
	expectTaskDefinition(client, workerTaskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// clusters are processed concurrently, so order isn't guaranteed
	assert.ElementsMatch(t, []string{
		fooTaskArn + " api 10.0.0.1:8080 /metrics http",
		barTaskArn + " worker 10.0.1.1:9100 /prom https",
	}, targetSummaries(got))
	client.AssertExpectations(t)
}

func TestDiscover_NoClusters(t *testing.T) {
	client := &mockEcsClient{}
	expectClusters(client)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// non-nil so the written file is `[]` rather than `null`
	assert.NotNil(t, got)
	assert.Empty(t, got)
	client.AssertExpectations(t)
}

func TestDiscover_NoScrapableContainers(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", nil))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	client.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestDiscover_InvalidLabelsAreLoggedAndSkipped(t *testing.T) {
	logs := captureLogs(t)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("abc", "/metrics", "http")),
		newContainerDefinition("worker", "example/worker:1.0", scrapeLabels("9100", "/prom", "https")))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"),
		newAwsvpcContainer("worker", taskArn+"/worker", "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// the misconfigured container doesn't stop the valid one in the same task
	assert.Equal(t, []string{taskArn + " worker 10.0.0.1:9100 /prom https"}, targetSummaries(got))
	assert.Contains(t, logs.String(), taskArn)
	assert.Contains(t, logs.String(), `"abc"`)
	client.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestDiscover_ContainerMissingFromTaskIsLoggedAndSkipped(t *testing.T) {
	logs := captureLogs(t)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")),
		newContainerDefinition("worker", "example/worker:1.0", scrapeLabels("9100", "/prom", "https")))
	// the task has no running "api" container
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("worker", taskArn+"/worker", "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{taskArn + " worker 10.0.0.1:9100 /prom https"}, targetSummaries(got))
	assert.Contains(t, logs.String(), fmt.Sprintf("Container %q of task %q not found", "api", taskArn))
	client.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestDiscover_ContainerWithoutIPIsLoggedAndSkipped(t *testing.T) {
	logs := captureLogs(t)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")))
	// e.g. a PENDING task whose network interface isn't attached yet
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", ""))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Contains(t, logs.String(), fmt.Sprintf("Container %q of task %q has no private IP", "api", taskArn))
	client.AssertExpectations(t)
}

func TestDiscover_UsesFirstNonEmptyIP(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	taskDef := newTaskDefinition(taskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")))
	container := newAwsvpcContainer("api", taskArn+"/api", "")
	container.NetworkInterfaces = []types.NetworkInterface{
		{PrivateIpv4Address: aws.String("")},
		{PrivateIpv4Address: aws.String("10.0.0.2")},
	}
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api", container)

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	expectTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{taskArn + " api 10.0.0.2:8080 /metrics http"}, targetSummaries(got))
	client.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestDiscover_DescribeTaskDefinitionErrorIsLoggedAndSkipped(t *testing.T) {
	logs := captureLogs(t)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	brokenTask1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	brokenTask2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	okTaskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/3"
	brokenTaskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/broken:1"
	okTaskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"
	apiErr := errors.New("describe task definition failed")

	okTaskDef := newTaskDefinition(okTaskDefArn, "api", 1,
		newContainerDefinition("api", "example/api:1.0", scrapeLabels("8080", "/metrics", "http")))
	brokenTask1 := newAwsvpcTask(clusterArn, brokenTask1Arn, brokenTaskDefArn, "service:broken",
		newAwsvpcContainer("app", brokenTask1Arn+"/app", "10.0.0.1"))
	brokenTask2 := newAwsvpcTask(clusterArn, brokenTask2Arn, brokenTaskDefArn, "service:broken",
		newAwsvpcContainer("app", brokenTask2Arn+"/app", "10.0.0.2"))
	okTask := newAwsvpcTask(clusterArn, okTaskArn, okTaskDefArn, "service:api",
		newAwsvpcContainer("api", okTaskArn+"/api", "10.0.0.3"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, brokenTask1, brokenTask2, okTask)
	// failures aren't cached: each task sharing the broken definition retries and logs
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(brokenTaskDefArn),
	}).Return(nil, apiErr).Times(2)
	expectTaskDefinition(client, okTaskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// one broken definition doesn't hide the other targets
	assert.Equal(t, []string{okTaskArn + " api 10.0.0.3:8080 /metrics http"}, targetSummaries(got))
	for _, taskArn := range []string{brokenTask1Arn, brokenTask2Arn} {
		assert.Contains(t, logs.String(), fmt.Sprintf("Failed describing task definition %s for task %s, skipping: %s", brokenTaskDefArn, taskArn, apiErr))
	}
	client.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestDiscover_DescribeTaskDefinitionWithoutDefinitionIsLoggedAndSkipped(t *testing.T) {
	logs := captureLogs(t)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"))

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	expectTasksInCluster(client, clusterArn, task)
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Contains(t, logs.String(), fmt.Sprintf("Failed describing task definition %s for task %s, skipping: no task definition returned", taskDefArn, taskArn))
	client.AssertExpectations(t)
}

func TestDiscover_ListClustersError(t *testing.T) {
	apiErr := errors.New("list clusters failed")

	client := &mockEcsClient{}
	client.On("ListClusters", mock.Anything, &ecs.ListClustersInput{}).Return(nil, apiErr).Once()

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got)
	client.AssertExpectations(t)
}

func TestDiscover_ListTasksError(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	apiErr := errors.New("list tasks failed")

	client := &mockEcsClient{}
	expectClusters(client, clusterArn)
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: aws.String(clusterArn),
	}).Return(nil, apiErr).Once()

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: testLabelConfig}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got)
	client.AssertExpectations(t)
}

