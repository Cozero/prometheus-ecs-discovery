package main

var testLabelConfig = ExplorerContainerLabelConfig{
	FilterLabel: "prometheus.io/scrape",
	PortLabel:   "prometheus.io/port",
	PathLabel:   "prometheus.io/path",
	SchemeLabel: "prometheus.io/scheme",
}
