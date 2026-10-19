package operations_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
)

func integrationConfig(t *testing.T) config.Tools {
	t.Helper()
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 and enable the configured tool for read-only real integration")
	}
	values, err := godotenv.Read("../../.env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cannot read backend/.env")
	}
	for key, value := range values {
		if _, ok := os.LookupEnv(key); !ok {
			t.Setenv(key, value)
		}
	}
	cfg, err := config.LoadTools()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPrometheusIntegration(t *testing.T) {
	cfg := integrationConfig(t)
	if !cfg.QueryPrometheusAlertsEnabled {
		t.Skip("Prometheus integration is disabled")
	}
	p, err := config.LoadPrometheus()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewPrometheus(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.Alerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Prometheus /api/v1/alerts succeeded; alerts=%d (no alert contents logged)", len(r.Alerts))
}

func TestLogMCPIntegration(t *testing.T) {
	cfg := integrationConfig(t)
	if !cfg.QueryLogEnabled {
		t.Skip("CLS MCP integration is disabled")
	}
	p, err := config.LoadLogMCP()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewLogMCP(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Logf("Streamable HTTP initialize and tools/list succeeded; tools=%d; SearchLog schema verified", len(c.Tools()))
	if p.Region == "" || p.TopicID == "" {
		t.Skip("provide ONCALL_LOG_REGION and ONCALL_LOG_TOPIC_ID for a real query")
	}
	service, err := operations.NewLogService(c, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	query := os.Getenv("ONCALL_LOG_INTEGRATION_QUERY")
	if query == "" {
		query = "*"
	}
	r, err := service.Query(context.Background(), &operations.LogInput{Query: query, StartTime: os.Getenv("ONCALL_LOG_INTEGRATION_START_TIME"), EndTime: os.Getenv("ONCALL_LOG_INTEGRATION_END_TIME")})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real SearchLog succeeded; from_ms=%d to_ms=%d limit=%d records=%d (no log contents logged)", r.Request.From, r.Request.To, r.Request.Limit, len(r.Records))
}
