package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/go-yaml/yaml"
)

// DescribeClusters accepts at most 100 clusters per call
const maxClusterIds = 100

// stringsFlag is a repeatable flag collecting one value per occurrence
type stringsFlag []string

func (s *stringsFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringsFlag) Set(value string) error {
	// ignore empty values, so e.g. `-config.cluster=` from an unset env var means "all clusters"
	if value = strings.TrimSpace(value); value != "" {
		*s = append(*s, value)
	}
	return nil
}

// appConfig is the tool's configuration, as parsed from the command line
type appConfig struct {
	clusterIds  []string
	outFile     string
	interval    time.Duration
	times       int
	roleArn     string
	labelConfig ExplorerContainerLabelConfig
}

// parseConfig parses command line arguments (without the program name); usage and parse errors go to output.
func parseConfig(args []string, output io.Writer) (appConfig, error) {
	commandLine := flag.NewFlagSet("prometheus-ecs-discovery", flag.ContinueOnError)
	commandLine.SetOutput(output)

	var cfg appConfig
	var clusterIds stringsFlag
	commandLine.Var(&clusterIds, "config.cluster", "name or ARN of a cluster to scrape; repeat for multiple clusters (up to 100 max; none = all clusters)")
	commandLine.StringVar(&cfg.outFile, "config.write-to", "ecs_file_sd.yml", "path of file to write ECS service discovery information to")
	commandLine.DurationVar(&cfg.interval, "config.scrape-interval", 60*time.Second, "interval at which to scrape the AWS API for ECS service discovery information")
	commandLine.IntVar(&cfg.times, "config.scrape-times", 0, "how many times to scrape before exiting (0 = infinite)")
	commandLine.StringVar(&cfg.roleArn, "config.role-arn", "", "ARN of the role to assume when scraping the AWS API (optional)")
	commandLine.StringVar(&cfg.labelConfig.FilterLabel, "config.filter-label", "prometheus.io/scrape", "Docker label that must be set to \"true\" for a container to be scraped")
	commandLine.StringVar(&cfg.labelConfig.PortLabel, "config.port-label", "prometheus.io/port", "Docker label to define the scrape port of the application (required)")
	commandLine.StringVar(&cfg.labelConfig.PathLabel, "config.path-label", "prometheus.io/path", "Docker label to define the scrape path of the application (required)")
	commandLine.StringVar(&cfg.labelConfig.SchemeLabel, "config.scheme-label", "prometheus.io/scheme", "Docker label to define the scheme (http or https) of the application (required)")

	if err := commandLine.Parse(args); err != nil {
		return appConfig{}, err
	}

	if len(clusterIds) > maxClusterIds {
		return appConfig{}, fmt.Errorf("at most %d clusters can be configured, got %d", maxClusterIds, len(clusterIds))
	}
	cfg.clusterIds = clusterIds

	return cfg, nil
}

// logError is a convenience function that decodes all possible ECS
// errors and displays them to standard error.
func logError(err error) {
	if err != nil {
		var oe *smithy.OperationError
		if errors.As(err, &oe) {
			log.Printf("failed to call service: %s, operation: %s, error: %v", oe.Service(), oe.Operation(), oe.Unwrap())
		} else {
			log.Println(err.Error())
		}
	}
}

// writeTargets writes the discovered targets as a Prometheus file service discovery config.
func writeTargets(path string, targets []*DiscoveredTaskTargets) error {
	m, err := yaml.Marshal(targets)
	if err != nil {
		return err
	}
	log.Printf("Writing %d discovered exporters to %s", len(targets), path)
	return ioutil.WriteFile(path, m, 0644)
}

// execute runs discovery once straight away, then on every tick, until cfg.times runs are done
// (0 = forever) or ctx is cancelled. A failed run is logged and doesn't stop the loop.
func execute(ctx context.Context, cfg appConfig, client EcsAPIClient, ticks <-chan time.Time) {
	explorer := &EcsTaskExplorer{
		ecs:                  client,
		containerLabelConfig: cfg.labelConfig,
		clusterIds:           cfg.clusterIds,
	}

	work := func() {
		targets, err := explorer.Discover(ctx)
		if err != nil {
			logError(err)
			return
		}
		if err := writeTargets(cfg.outFile, targets); err != nil {
			logError(err)
		}
	}

	work()
	for n := 1; cfg.times == 0 || n < cfg.times; n++ {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
		work()
	}
}

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		logError(err)
		return
	}

	if cfg.roleArn != "" {
		stsSvc := sts.NewFromConfig(awsCfg)
		awsCfg.Credentials = stscreds.NewAssumeRoleProvider(stsSvc, cfg.roleArn)
	}

	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()

	execute(context.Background(), cfg, ecs.NewFromConfig(awsCfg), ticker.C)
}
