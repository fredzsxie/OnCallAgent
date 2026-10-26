package operations

import (
	"context"
)

const PrometheusTool = "query_prometheus_alerts"

type Alert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    string            `json:"activeAt"`
	Value       string            `json:"value"`
}

type AlertsResult struct {
	Source string  `json:"source"`
	Alerts []Alert `json:"alerts"`
}

type AlertQuerier interface {
	Alerts(context.Context) (AlertsResult, error)
}
