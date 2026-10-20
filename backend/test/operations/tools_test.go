package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

type alertsFunc func(context.Context) (operations.AlertsResult, error)

func (f alertsFunc) Alerts(ctx context.Context) (operations.AlertsResult, error) { return f(ctx) }

func TestOperationsToolSchemasStrictInputAndSingleEncoding(t *testing.T) {
	registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true}, tools.Dependencies{
		Prometheus: alertsFunc(func(context.Context) (operations.AlertsResult, error) {
			return operations.AlertsResult{Source: "fixture", Alerts: []operations.Alert{}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`null`, `[]`, `{"url":"http://private"}`, `{} {}`} {
		if _, err := registry.Invoke(context.Background(), tools.QueryPrometheusAlerts, input); !errors.Is(err, operations.ErrInvalidInput) {
			t.Fatalf("input accepted: %s %v", input, err)
		}
	}
	result, err := registry.Invoke(context.Background(), tools.QueryPrometheusAlerts, `{}`)
	var output map[string]any
	if err != nil || json.Unmarshal([]byte(result), &output) != nil || output["alerts"] == nil {
		t.Fatalf("result double encoded: %s %v", result, err)
	}
	definitions, err := tools.Definitions(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true})
	if err != nil || len(definitions) != 1 {
		t.Fatal(err)
	}
	if !json.Valid(definitions[0].InputSchema) || !json.Valid(definitions[0].OutputSchema) {
		t.Fatal("invalid schemas")
	}
	var schema map[string]any
	_ = json.Unmarshal(definitions[0].InputSchema, &schema)
	if schema["type"] != "object" {
		t.Fatal("input must be object")
	}
	if _, err := registry.Invoke(context.Background(), tools.QueryInternalDocs, "{}"); !errors.Is(err, tools.ErrDisabledTool) {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "query_log", "{}"); !errors.Is(err, tools.ErrUnknownTool) {
		t.Fatal("removed tool should be unknown", err)
	}
}

func TestDisabledOperationsNeverLoadOrConnect(t *testing.T) {
	t.Setenv("ONCALL_PROMETHEUS_BASE_URL", "invalid")
	t.Setenv("ONCALL_PROMETHEUS_TIMEOUT", "")
	connections, err := client.OpenOperations(context.Background(), config.Tools{})
	if err != nil || connections.Prometheus != nil {
		t.Fatal("disabled tools opened clients", err)
	}
	defer connections.Close()
	registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC"}, tools.Dependencies{})
	if err != nil || len(registry.Tools()) != 0 {
		t.Fatal(err)
	}
	if _, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true}, tools.Dependencies{}); err == nil {
		t.Fatal("missing enabled dependency accepted")
	}
}
