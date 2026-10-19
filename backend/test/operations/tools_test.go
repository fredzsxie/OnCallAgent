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

func TestOperationsToolSchemasStrictInputAndSingleEncoding(t *testing.T) {
	cfg, _ := mcpFixture(t, mcpOptions{})
	mcp, err := client.NewLogMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer mcp.Close()
	logs, _ := operations.NewLogService(mcp, cfg, nil)
	registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryLogEnabled: true}, tools.Dependencies{Logs: logs})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`null`, `[]`, `{"query":"*","url":"http://private"}`, `{"query":"*"} {}`, `{"query":"*","limit":0}`} {
		if _, err := registry.Invoke(context.Background(), tools.QueryLog, input); !errors.Is(err, operations.ErrInvalidInput) {
			t.Fatalf("input accepted: %s %v", input, err)
		}
	}
	result, err := registry.Invoke(context.Background(), tools.QueryLog, `{"query":"*"}`)
	var output map[string]any
	if err != nil || json.Unmarshal([]byte(result), &output) != nil || output["request"] == nil || output["records"] == nil {
		t.Fatalf("result double encoded: %s %v", result, err)
	}
	definitions, err := tools.Definitions(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true, QueryLogEnabled: true})
	if err != nil || len(definitions) != 2 {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if !json.Valid(definition.InputSchema) || !json.Valid(definition.OutputSchema) {
			t.Fatal("invalid schemas")
		}
		var schema map[string]any
		_ = json.Unmarshal(definition.InputSchema, &schema)
		if schema["type"] != "object" {
			t.Fatal("input must be object")
		}
	}
	if _, err := registry.Invoke(context.Background(), tools.QueryPrometheusAlerts, "{}"); !errors.Is(err, tools.ErrDisabledTool) {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "unknown", "{}"); !errors.Is(err, tools.ErrUnknownTool) {
		t.Fatal(err)
	}
}

func TestDisabledOperationsNeverLoadOrConnect(t *testing.T) {
	t.Setenv("ONCALL_PROMETHEUS_BASE_URL", "invalid")
	t.Setenv("ONCALL_LOG_MCP_URL", "invalid")
	t.Setenv("ONCALL_PROMETHEUS_TIMEOUT", "")
	t.Setenv("ONCALL_LOG_MCP_TIMEOUT", "")
	connections, err := client.OpenOperations(context.Background(), config.Tools{})
	if err != nil || connections.Prometheus != nil || connections.MCP != nil {
		t.Fatal("disabled tools opened clients", err)
	}
	defer connections.Close()
	registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC"}, tools.Dependencies{})
	if err != nil || len(registry.Tools()) != 0 {
		t.Fatal(err)
	}
	if _, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryLogEnabled: true}, tools.Dependencies{}); err == nil {
		t.Fatal("missing enabled dependency accepted")
	}
	if _, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true}, tools.Dependencies{}); err == nil {
		t.Fatal("missing enabled dependency accepted")
	}
}
