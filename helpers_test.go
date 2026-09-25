package main

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/stretchr/testify/mock"
)

var testLabelConfig = ExplorerContainerLabelConfig{
	FilterLabel: "prometheus.io/scrape",
	PortLabel:   "prometheus.io/port",
	PathLabel:   "prometheus.io/path",
	SchemeLabel: "prometheus.io/scheme",
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

// setExpectationsListClusters mocks ListClusters API call returning results in a single page (i.e. no next page)
func setExpectationsListClusters(client *mockEcsClient, clusterArns ...string) {
	client.On("ListClusters", mock.Anything, &ecs.ListClustersInput{}).Return(&ecs.ListClustersOutput{
		ClusterArns: clusterArns,
	}, nil).Once()
}

// setExpectationsListAndDescribeTasks mocks a single page of ListTasks + DescribeTasks for the cluster.
func setExpectationsListAndDescribeTasks(client *mockEcsClient, clusterArn string, tasks ...types.Task) {
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

func setExpectationsDescribeTaskDefinition(client *mockEcsClient, taskDefinition types.TaskDefinition) {
	client.On("DescribeTaskDefinition", mock.Anything, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: taskDefinition.TaskDefinitionArn,
	}).Return(&ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &taskDefinition,
	}, nil).Once()
}