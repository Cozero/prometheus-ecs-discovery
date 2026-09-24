package main

import (
	"fmt"
	"strconv"

	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ExplorerContainerLabelConfig defines docker label names to scan to identify scrapable targets
type ExplorerContainerLabelConfig struct {
	FilterLabel string
	PortLabel   string
	PathLabel   string
	SchemeLabel string
}

// ContainerScrapeConfig defines scrape config for that container
type ContainerLabelTaskConfig struct {
	Port   int
	Path   string
	Scheme string
}

func (cf *ExplorerContainerLabelConfig) ContainerScrapeConfigFromDefinition(containerDef ecstypes.ContainerDefinition) (*ContainerLabelTaskConfig, error) {
	filterlabelValue, ok := containerDef.DockerLabels[cf.FilterLabel]
	if !ok {
		return nil, nil
	}
	// this is a reasonable expectation - that the value for this is supposed to be true
	if filterlabelValue != "true" {
		return nil, nil
	}

	// port
	portLabelValue, ok := containerDef.DockerLabels[cf.PortLabel]
	if !ok {
		return nil, nil
	}
	port, err := strconv.Atoi(portLabelValue)
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port label value for %s is out of valid port range %q", cf.PortLabel, portLabelValue)
	}

	// path
	path, ok := containerDef.DockerLabels[cf.PathLabel]
	if !ok {
		return nil, nil
	}
	if path == "" {
		return nil, fmt.Errorf("path label value for %s is an empty string", cf.PathLabel)
	}

	// scheme
	scheme, ok := containerDef.DockerLabels[cf.SchemeLabel]
	if !ok {
		return nil, nil
	}
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("scheme label value for %s is an invalid value %q", cf.SchemeLabel, scheme)
	}

	return &ContainerLabelTaskConfig{port, path, scheme}, nil
}
