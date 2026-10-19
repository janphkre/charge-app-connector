package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

type AppValue struct {
	description *prometheus.Desc
	retrieve    func(d *AppData) float64
}

type AppCollector struct {
	config *Configuration
	values []*AppValue
}

func newAppValue(name string, help string, f func(d *AppData) float64) *AppValue {
	return &AppValue{
		description: prometheus.NewDesc(name, help, nil, nil),
		retrieve:    f,
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func newAppCollector(config *Configuration) *AppCollector {
	return &AppCollector{
		config: config,
		values: []*AppValue{
			newAppValue("CA_Soc", "Percent state of charge of the car",
				func(d *AppData) float64 { return float64(d.Soc) }),
			newAppValue("CA_Range", "Kilometers remaining range of the car",
				func(d *AppData) float64 { return float64(d.Range) }),
			newAppValue("CA_Connected", "1 if the car is connected to a charger (status B or C)",
				func(d *AppData) float64 { return boolToFloat(d.Status == "B" || d.Status == "C") }),
			newAppValue("CA_Charging", "1 if the car is currently charging (status C)",
				func(d *AppData) float64 { return boolToFloat(d.Status == "C") }),
			newAppValue("CA_Climater", "1 if the climate control of the car is active",
				func(d *AppData) float64 { return boolToFloat(d.Climater) }),
			newAppValue("CA_Recent", "1 if the data shown in the car app is recent",
				func(d *AppData) float64 { return boolToFloat(d.IsRecent) }),
		},
	}
}

func (c *AppCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, value := range c.values {
		ch <- value.description
	}
}

// Collect never talks to the phone. It emits the most recent cached app data with
// the time it was captured as the sample timestamp; without cached data nothing is emitted.
func (c *AppCollector) Collect(ch chan<- prometheus.Metric) {
	data := c.config.Latest()
	if data == nil {
		return
	}
	for _, value := range c.values {
		ch <- prometheus.NewMetricWithTimestamp(
			data.CapturedAt,
			prometheus.MustNewConstMetric(value.description, prometheus.UntypedValue, value.retrieve(data)),
		)
	}
}
