package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := parseConfig(nil, ioutil.Discard)

	require.NoError(t, err)
	assert.Equal(t, appConfig{
		clusterIds: nil,
		outFile:    "ecs_file_sd.yml",
		interval:   60 * time.Second,
		times:      0,
		roleArn:    "",
		labelConfig: ExplorerContainerLabelConfig{
			FilterLabel: "prometheus.io/scrape",
			PortLabel:   "prometheus.io/port",
			PathLabel:   "prometheus.io/path",
			SchemeLabel: "prometheus.io/scheme",
		},
	}, cfg)
}

func TestParseConfig_AllFlags(t *testing.T) {
	cfg, err := parseConfig([]string{
		"-config.cluster=foo",
		"-config.write-to=/some/dir/out.yml",
		"-config.scrape-interval=30s",
		"-config.scrape-times=5",
		"-config.role-arn=arn:aws:iam::123456789012:role/discovery",
		"-config.filter-label=my.scrape",
		"-config.port-label=my.port",
		"-config.path-label=my.path",
		"-config.scheme-label=my.scheme",
	}, ioutil.Discard)

	require.NoError(t, err)
	assert.Equal(t, appConfig{
		clusterIds: []string{"foo"},
		outFile:    "/some/dir/out.yml",
		interval:   30 * time.Second,
		times:      5,
		roleArn:    "arn:aws:iam::123456789012:role/discovery",
		labelConfig: ExplorerContainerLabelConfig{
			FilterLabel: "my.scrape",
			PortLabel:   "my.port",
			PathLabel:   "my.path",
			SchemeLabel: "my.scheme",
		},
	}, cfg)
}

func TestParseConfig_RepeatedCluster(t *testing.T) {
	cfg, err := parseConfig([]string{"-config.cluster=foo", "-config.cluster=bar"}, ioutil.Discard)

	require.NoError(t, err)
	assert.Equal(t, []string{"foo", "bar"}, cfg.clusterIds)
}

func TestParseConfig_EmptyClusterValuesAreIgnored(t *testing.T) {
	cfg, err := parseConfig([]string{"-config.cluster=", "-config.cluster= foo ", "-config.cluster=  "}, ioutil.Discard)

	require.NoError(t, err)
	assert.Equal(t, []string{"foo"}, cfg.clusterIds)
}

func TestParseConfig_TooManyClusters(t *testing.T) {
	var args []string
	for i := 0; i <= maxClusterIds; i++ {
		args = append(args, fmt.Sprintf("-config.cluster=cluster-%d", i))
	}

	_, err := parseConfig(args, ioutil.Discard)

	assert.EqualError(t, err, "at most 100 clusters can be configured, got 101")
}

func TestParseConfig_UnknownFlag(t *testing.T) {
	// e.g. a flag the old version supported
	_, err := parseConfig([]string{"-config.dynamic-port-detection"}, ioutil.Discard)

	assert.Error(t, err)
}

func TestParseConfig_Help(t *testing.T) {
	_, err := parseConfig([]string{"-h"}, ioutil.Discard)

	assert.ErrorIs(t, err, flag.ErrHelp)
}

func TestExecute_WritesDiscoveredTargets(t *testing.T) {
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

	outFile := filepath.Join(t.TempDir(), "ecs_file_sd.yml")
	cfg := appConfig{outFile: outFile, times: 1, labelConfig: testLabelConfig}

	execute(context.Background(), cfg, client, nil)

	written, err := ioutil.ReadFile(outFile)
	require.NoError(t, err)
	// the file format is what Prometheus consumes, so it's pinned down exactly
	assert.Equal(t, `- targets:
  - 10.0.0.1:8080
  labels:
    task_arn: arn:aws:ecs:eu-central-1:123456789012:task/foo/1
    task_name: api
    task_revision: "3"
    task_group: service:api
    cluster_arn: arn:aws:ecs:eu-central-1:123456789012:cluster/foo
    container_name: api
    container_arn: arn:aws:ecs:eu-central-1:123456789012:container/foo/1/api
    docker_image: example/api:1.0
    __metrics_path__: /metrics
    __scheme__: http
`, string(written))
	client.AssertExpectations(t)
}

func TestExecute_NoTargetsWritesEmptyList(t *testing.T) {
	client := &mockEcsClient{}
	expectClusters(client)

	outFile := filepath.Join(t.TempDir(), "ecs_file_sd.yml")
	cfg := appConfig{outFile: outFile, times: 1, labelConfig: testLabelConfig}

	execute(context.Background(), cfg, client, nil)

	written, err := ioutil.ReadFile(outFile)
	require.NoError(t, err)
	// `[]` rather than `null`, so Prometheus sees an empty target list
	assert.Equal(t, "[]\n", string(written))
	client.AssertExpectations(t)
}

func TestExecute_DiscoveryErrorDoesNotWriteFile(t *testing.T) {
	client := &mockEcsClient{}
	client.On("ListClusters", mock.Anything, &ecs.ListClustersInput{}).Return(nil, errors.New("list clusters failed")).Once()

	outFile := filepath.Join(t.TempDir(), "ecs_file_sd.yml")
	cfg := appConfig{outFile: outFile, times: 1, labelConfig: testLabelConfig}

	execute(context.Background(), cfg, client, nil)

	// a failed run must not overwrite the last good targets with nothing
	assert.NoFileExists(t, outFile)
	client.AssertExpectations(t)
}

func TestExecute_RunsScrapeTimes(t *testing.T) {
	client := &mockEcsClient{}
	client.On("ListClusters", mock.Anything, &ecs.ListClustersInput{}).Return(&ecs.ListClustersOutput{}, nil).Times(3)

	cfg := appConfig{outFile: filepath.Join(t.TempDir(), "ecs_file_sd.yml"), times: 3, labelConfig: testLabelConfig}

	// first run is immediate, the other two wait for a tick each
	ticks := make(chan time.Time, 2)
	ticks <- time.Time{}
	ticks <- time.Time{}

	execute(context.Background(), cfg, client, ticks)

	client.AssertExpectations(t)
	assert.Empty(t, ticks, "every tick was consumed")
}

func TestExecute_RunsUntilCancelledWhenScrapeTimesIsZero(t *testing.T) {
	client := &mockEcsClient{}
	client.On("ListClusters", mock.Anything, &ecs.ListClustersInput{}).Return(&ecs.ListClustersOutput{}, nil).Times(3)

	cfg := appConfig{outFile: filepath.Join(t.TempDir(), "ecs_file_sd.yml"), times: 0, labelConfig: testLabelConfig}

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	go func() {
		// unbuffered: each send completes only once execute is waiting for the next tick
		ticks <- time.Time{}
		ticks <- time.Time{}
		cancel()
	}()

	execute(ctx, cfg, client, ticks)

	client.AssertExpectations(t)
}
