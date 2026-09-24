package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

type clusterResult struct {
	clusterArn string
	out        *ecs.DescribeTasksOutput
	err        error
}

type DetailedTaskData struct {
	task ecstypes.Task
	definition ecstypes.TaskDefinition
}

type DiscoveredTaskTargets struct {
	Targets []string `yaml:"targets"`
	Labels  labels   `yaml:"labels"`
}

// EcsTaskExplorer discovers ECS tasks
// Only supports tasks running with awsvpc network mode (for now)
type EcsTaskExplorer struct {
	ecs        EcsAPIClient
	ec2        Ec2APIClient

	containerLabelConfig ExplorerContainerLabelConfig
	clusterIds []string // empty array means all clusters. It will accept up to 100 entries (AWS API limit)
}

func (e *EcsTaskExplorer) Discover(ctx context.Context) ([]*DiscoveredTaskTargets, error) {
	clusterArns, err := e.GetClusterARNs(ctx)
	if err != nil {
		return nil, err
	}

	tasks, err := e.getDetailedTaskDataInClusters(ctx, clusterArns)
	if err != nil {
		return nil, err
	}

	// one entry per scrapable container: labels such as container name, path and scheme are per container
	allDiscoveredTaskTargets := []*DiscoveredTaskTargets{} // non-nil: marshals to [] not null
	for _, td := range tasks {
		for _, containerDef := range td.definition.ContainerDefinitions {
			scrapeConfig, err := e.containerLabelConfig.ContainerScrapeConfigFromDefinition(containerDef)
			if err != nil {
				// we don't stop processing - log errors
				log.Printf("Found issues processing label config for task %q of task definition %q: %s",
					aws.ToString(td.task.TaskArn), aws.ToString(td.definition.TaskDefinitionArn), err)
				continue
			}
			if scrapeConfig == nil {
				// not a scrape target
				continue
			}

			containerName := aws.ToString(containerDef.Name)
			container := findTaskContainer(td.task, containerName)
			if container == nil {
				log.Printf("Container %q of task %q not found in the running task, skipping", containerName, aws.ToString(td.task.TaskArn))
				continue
			}
			ip := containerPrivateIP(*container)
			if ip == "" {
				// e.g. task still PENDING, network interface not attached yet
				log.Printf("Container %q of task %q has no private IP yet, skipping", containerName, aws.ToString(td.task.TaskArn))
				continue
			}

			allDiscoveredTaskTargets = append(allDiscoveredTaskTargets, &DiscoveredTaskTargets{
				Targets: []string{fmt.Sprintf("%s:%d", ip, scrapeConfig.Port)},
				Labels: labels{
					TaskArn:       aws.ToString(td.task.TaskArn),
					TaskName:      aws.ToString(td.definition.Family),
					TaskRevision:  fmt.Sprintf("%d", td.definition.Revision),
					TaskGroup:     aws.ToString(td.task.Group),
					ClusterArn:    aws.ToString(td.task.ClusterArn),
					ContainerName: containerName,
					ContainerArn:  aws.ToString(container.ContainerArn),
					DockerImage:   aws.ToString(containerDef.Image),
					MetricsPath:   scrapeConfig.Path,
					Scheme:        scrapeConfig.Scheme,
				},
			})
		}
	}

	return allDiscoveredTaskTargets, nil
}

// findTaskContainer returns the running container with the given name, or nil if the task has none.
func findTaskContainer(task ecstypes.Task, name string) *ecstypes.Container {
	for i := range task.Containers {
		if aws.ToString(task.Containers[i].Name) == name {
			return &task.Containers[i]
		}
	}
	return nil
}

// containerPrivateIP returns the container's awsvpc private IPv4 address, or "" if it has none.
func containerPrivateIP(container ecstypes.Container) string {
	for _, ni := range container.NetworkInterfaces {
		if ip := aws.ToString(ni.PrivateIpv4Address); ip != "" {
			return ip
		}
	}
	return ""
}

// GetClusterARNs gets cluster ARNs
// this serves to validate the ARNs provided or fetch all clusters ARNs it has access to
func (e *EcsTaskExplorer) GetClusterARNs(ctx context.Context) ([]string, error) {
	var clusterArns []string

	if len(e.clusterIds) > 0 {
		res, err := e.ecs.DescribeClusters(ctx, &ecs.DescribeClustersInput{
			Clusters: e.clusterIds,
		})

		if err != nil {
			return nil, err
		}

		if len(res.Failures) > 0 {
			lines := make([]string, 0, len(res.Failures))
			for _, f := range res.Failures {
				lines = append(lines, fmt.Sprintf("- %s: %s", aws.ToString(f.Arn), aws.ToString(f.Reason)))
			}
			return nil, fmt.Errorf("failed to describe %d cluster(s):\n%s", len(res.Failures), strings.Join(lines, "\n"))
		}

		for _, c := range res.Clusters {
			clusterArns = append(clusterArns, *c.ClusterArn)
		}

	} else {
		res, err := e.getAllClusters(ctx)
		if err != nil {
			return nil, err
		}

		clusterArns = res.ClusterArns
	}

	return clusterArns, nil
}

// getAllClusters retrieves a list of all *ClusterArns from Amazon ECS,
// dealing with the mandatory pagination as needed.
func (e *EcsTaskExplorer) getAllClusters(ctx context.Context) (*ecs.ListClustersOutput, error) {
	input := &ecs.ListClustersInput{}
	listAllClusterResults := &ecs.ListClustersOutput{}
	for {
		resp, err := e.ecs.ListClusters(ctx, input)
		if err != nil {
			return nil, err
		}
		listAllClusterResults.ClusterArns = append(listAllClusterResults.ClusterArns, resp.ClusterArns...)
		if resp.NextToken == nil {
			break
		}
		input.NextToken = resp.NextToken
	}
	return listAllClusterResults, nil
}

func (e *EcsTaskExplorer) getDetailedTaskDataInClusters(ctx context.Context, clusterArns []string) ([]DetailedTaskData, error) {
	tasks, err := e.GetTasksInClusters(ctx, clusterArns)
	if err != nil {
		return nil, err
	}

	taskDefinitionsByArn := map[string]ecstypes.TaskDefinition{}

	detailedTaskData := make([]DetailedTaskData, 0, len(tasks))
	for _, task := range tasks {
		taskDefinitionArn := aws.ToString(task.TaskDefinitionArn)

		taskDefinition, existing := taskDefinitionsByArn[taskDefinitionArn]
		if !existing {
			res, err := e.ecs.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
				TaskDefinition: aws.String(taskDefinitionArn),
			})
			if err != nil {
				return nil, fmt.Errorf("Failed describing task definition %s: %w", taskDefinitionArn, err)
			}
			// while this should never happen, we're just doing defensive programming with this check
			if res.TaskDefinition == nil {
				return nil, fmt.Errorf("Failed describing task definition %s: no task definition returned", taskDefinitionArn)
			}

			taskDefinition = *res.TaskDefinition
			taskDefinitionsByArn[taskDefinitionArn] = taskDefinition
		}

		detailedTaskData = append(detailedTaskData, DetailedTaskData{
			task:       task,
			definition: taskDefinition,
		})
	}

	return detailedTaskData, nil
}

// GetTasksInClusters gets all tasks from a set of cluster ARNs
func (e *EcsTaskExplorer) GetTasksInClusters(ctx context.Context, clusterArns []string) ([]ecstypes.Task, error) {
	// create input and output channels
	jobs := make(chan string, len(clusterArns))
	results := make(chan clusterResult, len(clusterArns))

	// start goroutines to work through all cluster ARNs
	// TODO the number of goroutines should be configurable
	for w := 1; w <= 4; w++ {
		go func() {
			for clusterArn := range jobs {
				tasksInCluster, err := e.getTasksRunningInCluster(ctx, clusterArn)
				results <- clusterResult{clusterArn, tasksInCluster, err}
			}
		}()
	}

	// send cluster ARNs to jobs channel
	for _, clusterArn := range clusterArns {
		jobs <- clusterArn
	}
	close(jobs)

	// get all resulting tasks from the results channel
	tasks := []ecstypes.Task{}
	for range clusterArns {
		result := <-results
		if result.err != nil {
			return nil, result.err
		}

		// failures are tasks DescribeTasks couldn't describe, e.g. stopped between list and describe
		for _, f := range result.out.Failures {
			log.Printf("Failed to describe task %s in cluster %s: %s", aws.ToString(f.Arn), result.clusterArn, aws.ToString(f.Reason))
		}
		tasks = append(tasks, result.out.Tasks...)
	}

	return tasks, nil
}

// get tasks from a single cluster
func (e *EcsTaskExplorer) getTasksRunningInCluster(ctx context.Context, clusterArn string)(*ecs.DescribeTasksOutput, error) {
	listTaskInput := &ecs.ListTasksInput{
		Cluster: &clusterArn,
	}
	tasks := &ecs.DescribeTasksOutput{}

	for {
		listOutput, listErr := e.ecs.ListTasks(ctx, listTaskInput)
		if listErr != nil {
			return nil, listErr
		}

		descOutput, descErr := e.ecs.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: &clusterArn,
			Tasks:   listOutput.TaskArns,
		})
		if descErr != nil {
			return nil, descErr
		}
		
		tasks.Tasks = append(tasks.Tasks, descOutput.Tasks...)
		tasks.Failures = append(tasks.Failures, descOutput.Failures...)
		if listOutput.NextToken == nil {
			break
		}
		listTaskInput.NextToken = listOutput.NextToken
	}

	return tasks, nil
}
