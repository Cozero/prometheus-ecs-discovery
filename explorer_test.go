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

// mockEcsClient implements EcsAPIClient for tests.
type mockEcsClient struct {
	mock.Mock
}

// fails compilation if mockEcsClient stops implementing EcsAPIClient, e.g. when a method is added to it
var _ EcsAPIClient = (*mockEcsClient)(nil)

func (m *mockEcsClient) DescribeClusters(
	ctx context.Context, 
	in *ecs.DescribeClustersInput, 
	_ ...func(*ecs.Options),
) (*ecs.DescribeClustersOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.DescribeClustersOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) ListClusters(
	ctx context.Context, 
	in *ecs.ListClustersInput, 
	_ ...func(*ecs.Options),
) (*ecs.ListClustersOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.ListClustersOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) ListTasks(
	ctx context.Context, 
	in *ecs.ListTasksInput, 
	_ ...func(*ecs.Options),
) (*ecs.ListTasksOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.ListTasksOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) DescribeTasks(
	ctx context.Context, 
	in *ecs.DescribeTasksInput, 
	_ ...func(*ecs.Options),
) (*ecs.DescribeTasksOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.DescribeTasksOutput)
	return out, args.Error(1)
}

func (m *mockEcsClient) DescribeTaskDefinition(
	ctx context.Context, 
	in *ecs.DescribeTaskDefinitionInput, 
	_ ...func(*ecs.Options),
) (*ecs.DescribeTaskDefinitionOutput, error) {
	args := m.Called(ctx, in)
	out, _ := args.Get(0).(*ecs.DescribeTaskDefinitionOutput)
	return out, args.Error(1)
}

// mockContainerLabelConfig implements ContainerLabelConfig for tests.
type mockContainerLabelConfig struct {
	mock.Mock
}

func (m *mockContainerLabelConfig) ContainerScrapeConfigFromDefinition(
	containerDef types.ContainerDefinition,
) (*ContainerScrapeConfig, error) {
	args := m.Called(containerDef)
	out, _ := args.Get(0).(*ContainerScrapeConfig)
	return out, args.Error(1)
}

// scrapeDockerLabels returns docker labels that make a container a scrape target under testLabelConfig.
func scrapeDockerLabels(port string, path string, scheme string) map[string]string {
	return map[string]string{
		testLabelConfig.FilterLabel: "true",
		testLabelConfig.PortLabel:   port,
		testLabelConfig.PathLabel:   path,
		testLabelConfig.SchemeLabel: scheme,
	}
}

// newAwsvpcTask builds a RUNNING task; containers are optional.
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

func newContainerDefinition(name string, image string, dockerLabels map[string]string) types.ContainerDefinition {
	return types.ContainerDefinition{
		Name:         aws.String(name),
		Image:        aws.String(image),
		DockerLabels: dockerLabels,
	}
}

// newTaskDefinition builds a task definition identified by its ARN.
// Container definitions are optional and default to none.
func newTaskDefinition(
	taskDefinitionArn string,
	family string,
	revision int32,
	containerDefinitions ...types.ContainerDefinition,
) types.TaskDefinition {
	return types.TaskDefinition{
		TaskDefinitionArn:    aws.String(taskDefinitionArn),
		Family:               aws.String(family),
		Revision:             revision,
		ContainerDefinitions: containerDefinitions,
	}
}

/*
* ===================
* helper expectations
* ===================
*/

// setExpectationsDescribeClusters mocks DescribeClusters finding every cluster asked for, with no failures
func setExpectationsDescribeClusters(client *mockEcsClient, clusterIds []string, clusterArns ...string) {
	clusters := make([]types.Cluster, 0, len(clusterArns))
	for _, clusterArn := range clusterArns {
		clusters = append(clusters, types.Cluster{ClusterArn: aws.String(clusterArn)})
	}
	client.On("DescribeClusters", mock.Anything, &ecs.DescribeClustersInput{
		Clusters: clusterIds,
	}).Return(&ecs.DescribeClustersOutput{
		Clusters: clusters,
	}, nil).Once()
}

// setExpectationsListClusters mocks ListClusters API call returning results in a single page (i.e. no next page)
func setExpectationsListClusters(client *mockEcsClient, clusterArns ...string) {
	setExpectationsListClustersInPages(client, clusterArns)
}

// setExpectationsListClustersInPages mocks ListClusters returning one page per argument, chained by next tokens
func setExpectationsListClustersInPages(client *mockEcsClient, pages ...[]string) {
	for i, clusterArns := range pages {
		input := &ecs.ListClustersInput{}
		if i > 0 {
			input.NextToken = aws.String(fmt.Sprintf("page-%d", i+1))
		}
		output := &ecs.ListClustersOutput{ClusterArns: clusterArns}
		if i < len(pages)-1 {
			output.NextToken = aws.String(fmt.Sprintf("page-%d", i+2))
		}
		client.On("ListClusters", mock.Anything, input).Return(output, nil).Once()
	}
}

// setExpectationsListAndDescribeTasks mocks a single page of ListTasks + DescribeTasks for the cluster.
// With no tasks only ListTasks is mocked, as DescribeTasks isn't called for an empty page.
func setExpectationsListAndDescribeTasks(client *mockEcsClient, clusterArn string, tasks ...types.Task) {
	setExpectationsListAndDescribeTasksInPages(client, clusterArn, tasks)
}

// setExpectationsListAndDescribeTasksInPages mocks ListTasks returning one page per argument, chained by next tokens,
// plus DescribeTasks for each non-empty page.
func setExpectationsListAndDescribeTasksInPages(client *mockEcsClient, clusterArn string, pages ...[]types.Task) {
	for i, tasks := range pages {
		taskArns := make([]string, 0, len(tasks))
		for _, task := range tasks {
			taskArns = append(taskArns, aws.ToString(task.TaskArn))
		}

		input := &ecs.ListTasksInput{Cluster: aws.String(clusterArn)}
		if i > 0 {
			input.NextToken = aws.String(fmt.Sprintf("page-%d", i+1))
		}
		output := &ecs.ListTasksOutput{TaskArns: taskArns}
		if i < len(pages)-1 {
			output.NextToken = aws.String(fmt.Sprintf("page-%d", i+2))
		}
		client.On("ListTasks", mock.Anything, input).Return(output, nil).Once()

		if len(tasks) > 0 {
			client.On("DescribeTasks", mock.Anything, &ecs.DescribeTasksInput{
				Cluster: aws.String(clusterArn),
				Tasks:   taskArns,
			}).Return(&ecs.DescribeTasksOutput{
				Tasks: tasks,
			}, nil).Once()
		}
	}
}

func setExpectationsDescribeTaskDefinition(client *mockEcsClient, taskDefinition types.TaskDefinition) {
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

// captureLogs redirects the global logger for the rest of the test, so callers must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	var logs bytes.Buffer
	prevOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prevOutput) })
	return &logs
}

/*
* ===================
* TESTS
* ===================
*/

func TestDiscover_NoClusterIds_NoClustersFound(t *testing.T) {
	client := &mockEcsClient{}
	setExpectationsListClusters(client)

	labelConfig := &mockContainerLabelConfig{}

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// non-nil so the written file is `[]` rather than `null`
	assert.NotNil(t, got)
	assert.Empty(t, got)
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_NoClusterIds_ClustersWithoutTasks(t *testing.T) {
	fooClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	barClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/bar"

	client := &mockEcsClient{}
	// one cluster per page
	setExpectationsListClustersInPages(client, []string{fooClusterArn}, []string{barClusterArn})
	// no tasks, so no DescribeTasks either
	setExpectationsListAndDescribeTasks(client, fooClusterArn)
	setExpectationsListAndDescribeTasks(client, barClusterArn)

	// no expectations: there are no containers to check
	labelConfig := &mockContainerLabelConfig{}

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_WithClusterIds_AllTasksScrapable(t *testing.T) {
	fooClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	barClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/bar"
	fooTask1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	fooTask2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	barTask1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/bar/1"
	barTask2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/bar/2"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	containerDef := newContainerDefinition("api", "example/api:1.0", nil)
	taskDef := newTaskDefinition(taskDefArn, "api", 1, containerDef)
	fooTask1 := newAwsvpcTask(fooClusterArn, fooTask1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", fooTask1Arn+"/api", "10.0.0.1"))
	fooTask2 := newAwsvpcTask(fooClusterArn, fooTask2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", fooTask2Arn+"/api", "10.0.0.2"))
	barTask1 := newAwsvpcTask(barClusterArn, barTask1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", barTask1Arn+"/api", "10.0.1.1"))
	barTask2 := newAwsvpcTask(barClusterArn, barTask2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", barTask2Arn+"/api", "10.0.1.2"))

	client := &mockEcsClient{}
	setExpectationsDescribeClusters(client, []string{"foo", "bar"}, fooClusterArn, barClusterArn)
	// one task per page
	setExpectationsListAndDescribeTasksInPages(client, fooClusterArn, []types.Task{fooTask1}, []types.Task{fooTask2})
	setExpectationsListAndDescribeTasksInPages(client, barClusterArn, []types.Task{barTask1}, []types.Task{barTask2})
	// the same service runs in both clusters, so the shared definition is described once
	setExpectationsDescribeTaskDefinition(client, taskDef)

	labelConfig := &mockContainerLabelConfig{}
	// every task's container is checked
	labelConfig.On("ContainerScrapeConfigFromDefinition", containerDef).
		Return(&ContainerScrapeConfig{Port: 8080, Path: "/metrics", Scheme: "http"}, nil).Times(4)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig, clusterIds: []string{"foo", "bar"}}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// clusters are processed concurrently, so order isn't guaranteed
	assert.ElementsMatch(t, []string{
		fooTask1Arn + " api 10.0.0.1:8080 /metrics http",
		fooTask2Arn + " api 10.0.0.2:8080 /metrics http",
		barTask1Arn + " api 10.0.1.1:8080 /metrics http",
		barTask2Arn + " api 10.0.1.2:8080 /metrics http",
	}, targetSummaries(got))
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_NoClusterIds_AllTasksScrapable(t *testing.T) {
	fooClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	barClusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/bar"
	fooTask1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	fooTask2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	barTask1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/bar/1"
	barTask2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/bar/2"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	containerDef := newContainerDefinition("api", "example/api:1.0", nil)
	taskDef := newTaskDefinition(taskDefArn, "api", 1, containerDef)
	fooTask1 := newAwsvpcTask(fooClusterArn, fooTask1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", fooTask1Arn+"/api", "10.0.0.1"))
	fooTask2 := newAwsvpcTask(fooClusterArn, fooTask2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", fooTask2Arn+"/api", "10.0.0.2"))
	barTask1 := newAwsvpcTask(barClusterArn, barTask1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", barTask1Arn+"/api", "10.0.1.1"))
	barTask2 := newAwsvpcTask(barClusterArn, barTask2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", barTask2Arn+"/api", "10.0.1.2"))

	client := &mockEcsClient{}
	// one cluster per page
	setExpectationsListClustersInPages(client, []string{fooClusterArn}, []string{barClusterArn})
	// one task per page
	setExpectationsListAndDescribeTasksInPages(client, fooClusterArn, []types.Task{fooTask1}, []types.Task{fooTask2})
	setExpectationsListAndDescribeTasksInPages(client, barClusterArn, []types.Task{barTask1}, []types.Task{barTask2})
	// the same service runs in both clusters, so the shared definition is described once
	setExpectationsDescribeTaskDefinition(client, taskDef)

	labelConfig := &mockContainerLabelConfig{}
	// every task's container is checked
	labelConfig.On("ContainerScrapeConfigFromDefinition", containerDef).
		Return(&ContainerScrapeConfig{Port: 8080, Path: "/metrics", Scheme: "http"}, nil).Times(4)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// clusters are processed concurrently, so order isn't guaranteed
	assert.ElementsMatch(t, []string{
		fooTask1Arn + " api 10.0.0.1:8080 /metrics http",
		fooTask2Arn + " api 10.0.0.2:8080 /metrics http",
		barTask1Arn + " api 10.0.1.1:8080 /metrics http",
		barTask2Arn + " api 10.0.1.2:8080 /metrics http",
	}, targetSummaries(got))
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_WithClusterId_NoScrapableTasks(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	task3Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/3"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	containerDef := newContainerDefinition("api", "example/api:1.0", nil)
	taskDef := newTaskDefinition(taskDefArn, "api", 1, containerDef)
	task1 := newAwsvpcTask(clusterArn, task1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task1Arn+"/api", "10.0.0.1"))
	task2 := newAwsvpcTask(clusterArn, task2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task2Arn+"/api", "10.0.0.2"))
	task3 := newAwsvpcTask(clusterArn, task3Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task3Arn+"/api", "10.0.0.3"))

	client := &mockEcsClient{}
	setExpectationsDescribeClusters(client, []string{"foo"}, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task1, task2, task3)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	labelConfig := &mockContainerLabelConfig{}
	// every task's container is checked, none is a scrape target
	labelConfig.On("ContainerScrapeConfigFromDefinition", containerDef).Return(nil, nil).Times(3)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig, clusterIds: []string{"foo"}}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_WithClusterIds_DescribeClustersError(t *testing.T) {
	apiErr := errors.New("describe clusters failed")

	client := &mockEcsClient{}
	client.On("DescribeClusters", mock.Anything, &ecs.DescribeClustersInput{
		Clusters: []string{"foo", "bar"},
	}).Return(nil, apiErr).Once()

	// no expectations: fails before any container is checked
	labelConfig := &mockContainerLabelConfig{}

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig, clusterIds: []string{"foo", "bar"}}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got)
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_WithClusterIds_DescribeClustersFailures(t *testing.T) {
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

	// no expectations: fails before any container is checked
	labelConfig := &mockContainerLabelConfig{}

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig, clusterIds: []string{"foo", "does_not_exist"}}

	got, err := explorer.Discover(context.Background())

	assert.EqualError(t, err, fmt.Sprintf("failed to describe 1 cluster(s):\n- %s: %s", missingArn, missingReason))
	assert.Nil(t, got)
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_ListTasksErrorOnLaterPage(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	apiErr := errors.New("list tasks failed")

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)

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

	// no expectations: fails before any task definition is described or container checked
	labelConfig := &mockContainerLabelConfig{}

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got, "no partial results should be returned alongside an error")
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_DescribeTasksErrorOnLaterPage(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	apiErr := errors.New("describe tasks failed")

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)

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

	// no expectations: fails before any task definition is described or container checked
	labelConfig := &mockContainerLabelConfig{}

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got, "no partial results should be returned alongside an error")
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

// Swaps the global logger output, so it must not run in parallel with other tests.
func TestDiscover_DescribeTasksFailuresAreLogged(t *testing.T) {
	logs := captureLogs(t)

	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	task1Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	task2Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/2"
	task3Arn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/3"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:1"

	containerDef := newContainerDefinition("api", "example/api:1.0", nil)
	taskDef := newTaskDefinition(taskDefArn, "api", 1, containerDef)
	task1 := newAwsvpcTask(clusterArn, task1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task1Arn+"/api", "10.0.0.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
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
	setExpectationsDescribeTaskDefinition(client, taskDef)

	labelConfig := &mockContainerLabelConfig{}
	// only the described task's container is checked
	labelConfig.On("ContainerScrapeConfigFromDefinition", containerDef).
		Return(&ContainerScrapeConfig{Port: 8080, Path: "/metrics", Scheme: "http"}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: labelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{task1Arn + " api 10.0.0.1:8080 /metrics http"}, targetSummaries(got), "described tasks are still returned")
	assert.Contains(t, logs.String(), fmt.Sprintf("Failed to describe task %s in cluster %s: MISSING", task2Arn, clusterArn))
	assert.Contains(t, logs.String(), fmt.Sprintf("Failed to describe task %s in cluster %s: MISSING", task3Arn, clusterArn))
	client.AssertExpectations(t)
	labelConfig.AssertExpectations(t)
}

func TestDiscover_SingleScrapableContainer(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	taskArn := "arn:aws:ecs:eu-central-1:123456789012:task/foo/1"
	containerArn := "arn:aws:ecs:eu-central-1:123456789012:container/foo/1/api"
	taskDefArn := "arn:aws:ecs:eu-central-1:123456789012:task-definition/api:3"

	taskDef := newTaskDefinition(taskDefArn, "api", 3,
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", containerArn, "10.0.0.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")),
		newContainerDefinition("envoy", "envoyproxy/envoy:v1.20", nil))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"),
		newAwsvpcContainer("envoy", taskArn+"/envoy", "10.0.0.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")),
		newContainerDefinition("worker", "example/worker:1.0", scrapeDockerLabels("9100", "/prom", "https")))
	// awsvpc: containers in a task share the task's network interface
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"),
		newAwsvpcContainer("worker", taskArn+"/worker", "10.0.0.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")))
	task1 := newAwsvpcTask(clusterArn, task1Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task1Arn+"/api", "10.0.0.1"))
	task2 := newAwsvpcTask(clusterArn, task2Arn, taskDefArn, "service:api",
		newAwsvpcContainer("api", task2Arn+"/api", "10.0.0.2"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task1, task2)
	setExpectationsDescribeTaskDefinition(client, taskDef) // .Once(): shared definition is only described once

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")))
	workerTaskDef := newTaskDefinition(workerTaskDefArn, "worker", 1,
		newContainerDefinition("worker", "example/worker:1.0", scrapeDockerLabels("9100", "/prom", "https")))
	fooTask := newAwsvpcTask(fooClusterArn, fooTaskArn, apiTaskDefArn, "service:api",
		newAwsvpcContainer("api", fooTaskArn+"/api", "10.0.0.1"))
	barTask := newAwsvpcTask(barClusterArn, barTaskArn, workerTaskDefArn, "service:worker",
		newAwsvpcContainer("worker", barTaskArn+"/worker", "10.0.1.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, fooClusterArn, barClusterArn)
	setExpectationsListAndDescribeTasks(client, fooClusterArn, fooTask)
	setExpectationsListAndDescribeTasks(client, barClusterArn, barTask)
	setExpectationsDescribeTaskDefinition(client, apiTaskDef)
	setExpectationsDescribeTaskDefinition(client, workerTaskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

	got, err := explorer.Discover(context.Background())

	require.NoError(t, err)
	// clusters are processed concurrently, so order isn't guaranteed
	assert.ElementsMatch(t, []string{
		fooTaskArn + " api 10.0.0.1:8080 /metrics http",
		barTaskArn + " worker 10.0.1.1:9100 /prom https",
	}, targetSummaries(got))
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
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("abc", "/metrics", "http")),
		newContainerDefinition("worker", "example/worker:1.0", scrapeDockerLabels("9100", "/prom", "https")))
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", "10.0.0.1"),
		newAwsvpcContainer("worker", taskArn+"/worker", "10.0.0.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")),
		newContainerDefinition("worker", "example/worker:1.0", scrapeDockerLabels("9100", "/prom", "https")))
	// the task has no running "api" container
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("worker", taskArn+"/worker", "10.0.0.1"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")))
	// e.g. a PENDING task whose network interface isn't attached yet
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api",
		newAwsvpcContainer("api", taskArn+"/api", ""))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")))
	container := newAwsvpcContainer("api", taskArn+"/api", "")
	container.NetworkInterfaces = []types.NetworkInterface{
		{PrivateIpv4Address: aws.String("")},
		{PrivateIpv4Address: aws.String("10.0.0.2")},
	}
	task := newAwsvpcTask(clusterArn, taskArn, taskDefArn, "service:api", container)

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	setExpectationsDescribeTaskDefinition(client, taskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
		newContainerDefinition("api", "example/api:1.0", scrapeDockerLabels("8080", "/metrics", "http")))
	brokenTask1 := newAwsvpcTask(clusterArn, brokenTask1Arn, brokenTaskDefArn, "service:broken",
		newAwsvpcContainer("app", brokenTask1Arn+"/app", "10.0.0.1"))
	brokenTask2 := newAwsvpcTask(clusterArn, brokenTask2Arn, brokenTaskDefArn, "service:broken",
		newAwsvpcContainer("app", brokenTask2Arn+"/app", "10.0.0.2"))
	okTask := newAwsvpcTask(clusterArn, okTaskArn, okTaskDefArn, "service:api",
		newAwsvpcContainer("api", okTaskArn+"/api", "10.0.0.3"))

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, brokenTask1, brokenTask2, okTask)
	// failures aren't cached: each task sharing the broken definition retries and logs
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(brokenTaskDefArn),
	}).Return(nil, apiErr).Times(2)
	setExpectationsDescribeTaskDefinition(client, okTaskDef)

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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
	setExpectationsListClusters(client, clusterArn)
	setExpectationsListAndDescribeTasks(client, clusterArn, task)
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefArn),
	}).Return(&ecs.DescribeTaskDefinitionOutput{}, nil).Once()

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

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

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got)
	client.AssertExpectations(t)
}

func TestDiscover_ListTasksError(t *testing.T) {
	clusterArn := "arn:aws:ecs:eu-central-1:123456789012:cluster/foo"
	apiErr := errors.New("list tasks failed")

	client := &mockEcsClient{}
	setExpectationsListClusters(client, clusterArn)
	client.On("ListTasks", mock.Anything, &ecs.ListTasksInput{
		Cluster: aws.String(clusterArn),
	}).Return(nil, apiErr).Once()

	explorer := &EcsTaskExplorer{ecs: client, containerLabelConfig: &testLabelConfig}

	got, err := explorer.Discover(context.Background())

	assert.ErrorIs(t, err, apiErr)
	assert.Nil(t, got)
	client.AssertExpectations(t)
}
