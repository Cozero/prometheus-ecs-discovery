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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockExplorer implements Explorer for tests.
type mockExplorer struct {
	mock.Mock
}

func (m *mockExplorer) Discover(ctx context.Context) ([]*DiscoveredTaskTargets, error) {
	args := m.Called(ctx)
	out, _ := args.Get(0).([]*DiscoveredTaskTargets)
	return out, args.Error(1)
}

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
		"-config.cluster=foo-cluster",
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
		clusterIds: []string{"foo-cluster"},
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

func TestParseConfig_MultipleSpecificClusters(t *testing.T) {
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
	_, err := parseConfig([]string{"-config.dynamic-port-detection"}, ioutil.Discard)

	assert.Error(t, err)
}

func TestParseConfig_Help(t *testing.T) {
	_, err := parseConfig([]string{"-h"}, ioutil.Discard)

	assert.ErrorIs(t, err, flag.ErrHelp)
}

func TestExecute_WritesDiscoveredTargets(t *testing.T) {
	explorer := &mockExplorer{}
	explorer.On("Discover", mock.Anything).Return([]*DiscoveredTaskTargets{
		{
			Targets: []string{"10.0.0.1:8080"},
			Labels: labels{
				TaskArn:       "arn:aws:ecs:eu-central-1:123456789012:task/cluster-foo/12345",
				TaskName:      "my-api",
				TaskRevision:  "3",
				TaskGroup:     "service:my-api",
				ClusterArn:    "arn:aws:ecs:eu-central-1:123456789012:cluster/cluster-foo",
				ContainerName: "api",
				ContainerArn:  "arn:aws:ecs:eu-central-1:123456789012:container/cluster-foo/12345/some-uuid",
				DockerImage:   "MyOrg/my-api:1.0",
				MetricsPath:   "/api/metrics",
				Scheme:        "https",
			},
		},
		{
			Targets: []string{"10.1.3.187:3000"},
			Labels: labels{
				TaskArn:       "arn:aws:ecs:eu-central-1:123456789012:task/cluster-bar/67890",
				TaskName:      "async-worker",
				TaskRevision:  "1017",
				TaskGroup:     "service:async-worker",
				ClusterArn:    "arn:aws:ecs:eu-central-1:123456789012:cluster/cluster-bar",
				ContainerName: "worker",
				ContainerArn:  "arn:aws:ecs:eu-central-1:123456789012:container/cluster-bar/67890/another-uuid",
				DockerImage:   "AnotherOrg/async-worker:0.0.1",
				MetricsPath:   "/metrics",
				Scheme:        "http",
			},
		},
	}, nil).Once()

	outFile := filepath.Join(t.TempDir(), "ecs_file_sd.yml")
	cfg := appConfig{outFile: outFile, times: 1}

	execute(context.Background(), cfg, explorer, nil)

	written, err := ioutil.ReadFile(outFile)
	require.NoError(t, err)
	// the file format is what Prometheus consumes, so it's pinned down exactly
	assert.Equal(t, `- targets:
  - 10.0.0.1:8080
  labels:
    task_arn: arn:aws:ecs:eu-central-1:123456789012:task/cluster-foo/12345
    task_name: my-api
    task_revision: "3"
    task_group: service:my-api
    cluster_arn: arn:aws:ecs:eu-central-1:123456789012:cluster/cluster-foo
    container_name: api
    container_arn: arn:aws:ecs:eu-central-1:123456789012:container/cluster-foo/12345/some-uuid
    docker_image: MyOrg/my-api:1.0
    __metrics_path__: /api/metrics
    __scheme__: https
- targets:
  - 10.1.3.187:3000
  labels:
    task_arn: arn:aws:ecs:eu-central-1:123456789012:task/cluster-bar/67890
    task_name: async-worker
    task_revision: "1017"
    task_group: service:async-worker
    cluster_arn: arn:aws:ecs:eu-central-1:123456789012:cluster/cluster-bar
    container_name: worker
    container_arn: arn:aws:ecs:eu-central-1:123456789012:container/cluster-bar/67890/another-uuid
    docker_image: AnotherOrg/async-worker:0.0.1
    __metrics_path__: /metrics
    __scheme__: http
`, string(written))
	explorer.AssertExpectations(t)
}

func TestExecute_NoTargets_WritesEmptyList(t *testing.T) {
	explorer := &mockExplorer{}
	explorer.On("Discover", mock.Anything).Return([]*DiscoveredTaskTargets{}, nil).Once()

	outFile := filepath.Join(t.TempDir(), "ecs_file_sd.yml")
	cfg := appConfig{outFile: outFile, times: 1}

	execute(context.Background(), cfg, explorer, nil)

	written, err := ioutil.ReadFile(outFile)
	require.NoError(t, err)
	// `[]` rather than `null`, so Prometheus sees an empty target list
	assert.Equal(t, "[]\n", string(written))
	explorer.AssertExpectations(t)
}

func TestExecute_DiscoveryErrorDoesNotWriteFile(t *testing.T) {
	explorer := &mockExplorer{}
	explorer.On("Discover", mock.Anything).Return(nil, errors.New("discovery failed")).Once()

	outFile := filepath.Join(t.TempDir(), "ecs_file_sd.yml")
	cfg := appConfig{outFile: outFile, times: 1}

	execute(context.Background(), cfg, explorer, nil)

	// a failed run must not overwrite the last good targets with nothing
	assert.NoFileExists(t, outFile)
	explorer.AssertExpectations(t)
}

func TestExecute_RunsScrapeTimes(t *testing.T) {
	explorer := &mockExplorer{}
	explorer.On("Discover", mock.Anything).Return([]*DiscoveredTaskTargets{}, nil).Times(3)

	cfg := appConfig{outFile: filepath.Join(t.TempDir(), "ecs_file_sd.yml"), times: 3}

	// first run is immediate, the other two wait for a tick each
	ticks := make(chan time.Time, 2)
	ticks <- time.Time{}
	ticks <- time.Time{}

	execute(context.Background(), cfg, explorer, ticks)

	explorer.AssertExpectations(t)
	assert.Empty(t, ticks, "every tick was consumed")
}

func TestExecute_WhenScrapeTimesIsZero_RunsUntilCancelled(t *testing.T) {
	explorer := &mockExplorer{}
	explorer.On("Discover", mock.Anything).Return([]*DiscoveredTaskTargets{}, nil).Times(3)

	cfg := appConfig{outFile: filepath.Join(t.TempDir(), "ecs_file_sd.yml"), times: 0}

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	go func() {
		// unbuffered: each send completes only once execute is waiting for the next tick
		ticks <- time.Time{}
		ticks <- time.Time{}
		cancel()
	}()

	execute(ctx, cfg, explorer, ticks)

	explorer.AssertExpectations(t)
}
