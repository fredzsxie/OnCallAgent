package operations

import (
	"context"
	"encoding/json"
)

const (
	PrometheusTool = "query_prometheus_alerts"
	LogTool        = "query_log"
)

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

type LogInput struct {
	Query     string `json:"query" jsonschema:"required,description=CLS CQL query; use * to match all logs"`
	Region    string `json:"region,omitempty" jsonschema:"description=CLS region; defaults to configured region"`
	TopicID   string `json:"topic_id,omitempty" jsonschema:"description=Single CLS topic ID; defaults to configured topic"`
	StartTime string `json:"start_time,omitempty" jsonschema:"description=RFC3339 start time with time zone; provide both start and end"`
	EndTime   string `json:"end_time,omitempty" jsonschema:"description=RFC3339 end time with time zone"`
	Limit     *int   `json:"limit,omitempty" jsonschema:"description=Maximum returned records between 1 and 1000"`
}

type LogRequest struct {
	Region  string `json:"region"`
	TopicID string `json:"topic_id"`
	Query   string `json:"query"`
	From    int64  `json:"from"`
	To      int64  `json:"to"`
	Limit   int    `json:"limit"`
}

type LogResult struct {
	Request LogRequest        `json:"request"`
	Records []json.RawMessage `json:"records"`
}

type LogSearcher interface {
	Search(context.Context, LogRequest) ([]json.RawMessage, error)
}

type LogQuerier interface {
	Query(context.Context, *LogInput) (LogResult, error)
}
