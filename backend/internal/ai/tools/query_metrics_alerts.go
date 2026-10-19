package tools

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

const QueryPrometheusAlerts = operations.PrometheusTool
const alertsDescription = "Query active Prometheus alerts. Results preserve each alert instance, labels, annotations, state, activation time, value and source. An empty alerts array means no active alerts."

type PrometheusAlertsInput struct{}

func NewPrometheusAlertsQueryTool(client operations.AlertQuerier) (tool.InvokableTool, error) {
	if client == nil {
		return nil, operations.Failure(QueryPrometheusAlerts, operations.ErrConfiguration)
	}
	return newOperationTool(QueryPrometheusAlerts, alertsDescription, func(ctx context.Context, _ *PrometheusAlertsInput) (operations.AlertsResult, error) {
		return client.Alerts(ctx)
	})
}
