package main

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testLabelConfig = ExplorerContainerLabelConfig{
	FilterLabel: "prometheus.io/scrape",
	PortLabel:   "prometheus.io/port",
	PathLabel:   "prometheus.io/path",
	SchemeLabel: "prometheus.io/scheme",
}

// validLabels returns a fresh set of labels that match testLabelConfig,
// with overrides applied on top. An override to nil removes the label.
func validLabels(overrides map[string]*string) map[string]string {
	labels := map[string]string{
		testLabelConfig.FilterLabel: "true",
		testLabelConfig.PortLabel:   "8080",
		testLabelConfig.PathLabel:   "/metrics",
		testLabelConfig.SchemeLabel: "http",
	}
	for k, v := range overrides {
		if v == nil {
			delete(labels, k)
		} else {
			labels[k] = *v
		}
	}
	return labels
}

func TestContainerScrapeConfigFromDefinition(t *testing.T) {
	validConfig := &ContainerLabelTaskConfig{Port: 8080, Path: "/metrics", Scheme: "http"}

	tests := []struct {
		name   string
		labels map[string]string
		want   *ContainerLabelTaskConfig
		// wantErr is a substring expected in the error; empty means no error
		wantErr string
	}{
		// match
		{name: "all labels for valid scrape config", labels: validLabels(nil), 
			want: validConfig},
		{name: "unrelated labels are ignored", labels: validLabels(map[string]*string{"OTHER": aws.String("x")}), 
			want: validConfig},
		{name: "lowest valid port", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("1")}),
			want: &ContainerLabelTaskConfig{Port: 1, Path: "/metrics", Scheme: "http"}},
		{name: "highest valid port", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("65535")}),
			want: &ContainerLabelTaskConfig{Port: 65535, Path: "/metrics", Scheme: "http"}},
		{name: "http scheme", labels: validLabels(map[string]*string{testLabelConfig.SchemeLabel: aws.String("http")}),
			want: &ContainerLabelTaskConfig{Port: 8080, Path: "/metrics", Scheme: "http"}},
		{name: "https scheme", labels: validLabels(map[string]*string{testLabelConfig.SchemeLabel: aws.String("https")}),
			want: &ContainerLabelTaskConfig{Port: 8080, Path: "/metrics", Scheme: "https"}},


		// not a scrape target: no config, no error
		{name: "no labels", labels: nil, 
			want: nil},
		{name: "filter label missing", labels: validLabels(map[string]*string{testLabelConfig.FilterLabel: nil}), 
			want: nil},
		{name: "filter label false", labels: validLabels(map[string]*string{testLabelConfig.FilterLabel: aws.String("false")}),
			want: nil},
		{name: "filter label empty", labels: validLabels(map[string]*string{testLabelConfig.FilterLabel: aws.String("")}),
			want: nil},
		{name: "filter label is case sensitive", labels: validLabels(map[string]*string{testLabelConfig.FilterLabel: aws.String("True")}),
			want: nil},
		{name: "port label missing", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: nil}),
			want: nil},
		{name: "path label missing", labels: validLabels(map[string]*string{testLabelConfig.PathLabel: nil}),
			want: nil},
		{name: "scheme label missing", labels: validLabels(map[string]*string{testLabelConfig.SchemeLabel: nil}),
			want: nil},

		// misconfigured path: no config, error mentioning the label
		{name: "path label empty", labels: validLabels(map[string]*string{testLabelConfig.PathLabel: aws.String("")}), 
			want: nil, wantErr: testLabelConfig.PathLabel},

		// unsupported scheme: no config, error mentioning the offending value
		{name: "scheme label empty", labels: validLabels(map[string]*string{testLabelConfig.SchemeLabel: aws.String("")}), 
			want: nil, wantErr: `""`},
		{name: "scheme not http(s)", labels: validLabels(map[string]*string{testLabelConfig.SchemeLabel: aws.String("ftp")}), 
			want: nil, wantErr: `"ftp"`},
		{name: "scheme is case sensitive", labels: validLabels(map[string]*string{testLabelConfig.SchemeLabel: aws.String("HTTP")}), 
			want: nil, wantErr: `"HTTP"`},

		// invalid port: no config, error mentioning the offending value
		{name: "port label empty", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("")}), 
			want: nil, wantErr: `""`},
		{name: "port not a number", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("abc")}), 
			want: nil, wantErr: `"abc"`},
		{name: "port with whitespace", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String(" 8080")}), 
			want: nil, wantErr: `" 8080"`},
		{name: "port zero", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("0")}), 
			want: nil, wantErr: `"0"`},
		{name: "port negative", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("-1")}), 
			want: nil, wantErr: `"-1"`},
		{name: "port above range", labels: validLabels(map[string]*string{testLabelConfig.PortLabel: aws.String("65536")}), 
			want: nil, wantErr: `"65536"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testLabelConfig.ContainerScrapeConfigFromDefinition(ecstypes.ContainerDefinition{
				Name:         aws.String("api"),
				DockerLabels: tt.labels,
			})

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContainerScrapeConfigFromDefinition_CustomLabelNames(t *testing.T) {
	cfg := ExplorerContainerLabelConfig{
		FilterLabel: "my.scrape",
		PortLabel:   "my.port",
		PathLabel:   "my.path",
		SchemeLabel: "my.scheme",
	}

	got, err := cfg.ContainerScrapeConfigFromDefinition(ecstypes.ContainerDefinition{
		Name: aws.String("api"),
		DockerLabels: map[string]string{
			"my.scrape": "true",
			"my.port":   "9100",
			"my.path":   "/prom",
			"my.scheme": "https",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, &ContainerLabelTaskConfig{Port: 9100, Path: "/prom", Scheme: "https"}, got)
}
