package main

import (
	"context"
	"errors"
	"flag"
	"io/ioutil"
	"log"
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

var clusterIds stringsFlag

func init() {
	flag.Var(&clusterIds, "config.cluster", "name or ARN of a cluster to scrape; repeat for multiple clusters (none = all clusters, max 100)")
}

var outFile = flag.String("config.write-to", "ecs_file_sd.yml", "path of file to write ECS service discovery information to")
var interval = flag.Duration("config.scrape-interval", 60*time.Second, "interval at which to scrape the AWS API for ECS service discovery information")
var times = flag.Int("config.scrape-times", 0, "how many times to scrape before exiting (0 = infinite)")
var roleArn = flag.String("config.role-arn", "", "ARN of the role to assume when scraping the AWS API (optional)")
var prometheusFilterLabel = flag.String("config.filter-label", "prometheus.io/scrape", "Docker label that must be set to \"true\" for a container to be scraped")
var prometheusPortLabel = flag.String("config.port-label", "prometheus.io/port", "Docker label to define the scrape port of the application (required)")
var prometheusPathLabel = flag.String("config.path-label", "prometheus.io/path", "Docker label to define the scrape path of the application (required)")
var prometheusSchemeLabel = flag.String("config.scheme-label", "prometheus.io/scheme", "Docker label to define the scheme (http or https) of the application (required)")

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

func main() {
	flag.Parse()

	if len(clusterIds) > maxClusterIds {
		log.Fatalf("at most %d clusters can be configured, got %d", maxClusterIds, len(clusterIds))
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		logError(err)
		return
	}

	if *roleArn != "" {
		// Assume role
		stsSvc := sts.NewFromConfig(cfg)
		cfg.Credentials = stscreds.NewAssumeRoleProvider(stsSvc, *roleArn)
	}

	explorer := &EcsTaskExplorer{
		ecs: ecs.NewFromConfig(cfg),
		containerLabelConfig: ExplorerContainerLabelConfig{
			FilterLabel: *prometheusFilterLabel,
			PortLabel:   *prometheusPortLabel,
			PathLabel:   *prometheusPathLabel,
			SchemeLabel: *prometheusSchemeLabel,
		},
		clusterIds: clusterIds,
	}

	work := func() {
		targets, err := explorer.Discover(context.Background())
		if err != nil {
			logError(err)
			return
		}
		m, err := yaml.Marshal(targets)
		if err != nil {
			logError(err)
			return
		}
		log.Printf("Writing %d discovered exporters to %s", len(targets), *outFile)
		err = ioutil.WriteFile(*outFile, m, 0644)
		if err != nil {
			logError(err)
			return
		}
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// run once straight away, then on every tick until scrape-times runs are done (0 = forever)
	work()
	for n := 1; *times == 0 || n < *times; n++ {
		<-ticker.C
		work()
	}
}
