package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func stdioConfig(args ...string) config.LogMCP {
	return config.LogMCP{Command: mcpFixtureBinary, Args: args, Timeout: 2 * time.Second,
		SecretID: "fixture-id", SecretKey: "fixture-key", TimeZone: "Asia/Shanghai",
		Region: "ap-guangzhou", TopicID: "fixture-topic", Lookback: 15 * time.Minute, Limit: 20}
}

func TestMCPStdioSearchSchemaEnvironmentAndClose(t *testing.T) {
	t.Setenv("ONCALL_CHAT_MODEL_API_KEY", "should-not-be-inherited")
	t.Setenv("ONCALL_MILVUS_PASSWORD", "should-not-be-inherited")
	cfg := stdioConfig()
	c, err := client.NewLogMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defs := c.Tools()
	if len(defs) != 1 || defs[0].Name != "SearchLog" || !json.Valid(defs[0].InputSchema) {
		t.Fatalf("unexpected stdio tool list: %+v", defs)
	}
	service, err := operations.NewLogService(c, cfg, func() time.Time { return time.Date(2026, 10, 19, 12, 15, 0, 0, time.FixedZone("UTC+8", 8*3600)) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(context.Background(), &operations.LogInput{Query: "level:ERROR"})
	if err != nil || len(result.Records) != 1 {
		t.Fatalf("stdio SearchLog result: %+v %v", result, err)
	}
	var record struct {
		Received          map[string]any `json:"received"`
		HasModelKey       bool           `json:"has_model_key"`
		HasMilvusPassword bool           `json:"has_milvus_password"`
		SecretID          string         `json:"secret_id"`
		SecretKey         string         `json:"secret_key"`
		Transport         string         `json:"transport"`
	}
	if err := json.Unmarshal(result.Records[0], &record); err != nil {
		t.Fatal(err)
	}
	if record.Received["Region"] != "ap-guangzhou" || record.Received["TopicId"] != "fixture-topic" || record.Received["Query"] != "level:ERROR" || record.Received["From"] != float64(1792382400000) || record.Received["To"] != float64(1792383300000) || record.Received["Limit"] != float64(20) {
		t.Fatalf("SearchLog args were not passed unchanged: %+v", record.Received)
	}
	if record.HasModelKey || record.HasMilvusPassword || record.SecretID != cfg.SecretID || record.SecretKey != cfg.SecretKey || record.Transport != "stdio" {
		t.Fatalf("unexpected child environment: %+v", record)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMCPStdioRejectsSchemaAndStartFailure(t *testing.T) {
	if _, err := client.NewLogMCP(context.Background(), stdioConfig("--bad-schema")); !errors.Is(err, operations.ErrInvalidResponse) {
		t.Fatalf("incompatible schema accepted: %v", err)
	}
	exited := stdioConfig("--exit-before-handshake")
	exited.Timeout = 100 * time.Millisecond
	if _, err := client.NewLogMCP(context.Background(), exited); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("early process exit not reported: %v", err)
	}
	cfg := stdioConfig()
	cfg.Command = "/missing/oncall/mcp/command"
	if _, err := client.NewLogMCP(context.Background(), cfg); !errors.Is(err, operations.ErrConfiguration) {
		t.Fatalf("missing command not reported safely: %v", err)
	}
}

func TestMCPStdioTimeoutAndCancellation(t *testing.T) {
	cfg := stdioConfig("--hang-call")
	cfg.Timeout = 100 * time.Millisecond
	c, err := client.NewLogMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Search(context.Background(), operations.LogRequest{Region: cfg.Region, TopicID: cfg.TopicID, Query: "*", From: 1, To: 2, Limit: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stdio timeout was not preserved: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	cfg.Timeout = time.Second
	c, err = client.NewLogMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Search(ctx, operations.LogRequest{Region: cfg.Region, TopicID: cfg.TopicID, Query: "*", From: 1, To: 2, Limit: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stdio cancellation was not preserved: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
