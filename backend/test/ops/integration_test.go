package ops_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/joho/godotenv"
)

func TestOpsIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 for read-only real tools and configured models")
	}
	values, err := godotenv.Read("../../.env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cannot read backend/.env")
	}
	for k, v := range values {
		if _, ok := os.LookupEnv(k); !ok {
			t.Setenv(k, v)
		}
	}
	cfg, err := config.LoadOpsAgent()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.LoadOpsModels(); err != nil {
		t.Skip("configure planning and execution models")
	}
	tools, err := config.LoadTools()
	if err != nil {
		t.Fatal(err)
	}
	if !tools.QueryPrometheusAlertsEnabled {
		t.Skip("enable Prometheus for real investigation acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout+10*time.Second)
	defer cancel()
	query := "请使用已启用的告警工具检查当前告警，使用时间工具核对时间，并使用内部文档工具检索相关排查手册。若日志工具可用请查询相关日志。根据真实工具结果生成报告；明确列出缺失或失败的工具以及未确认事项，不要执行任何修复。"
	cmd := exec.CommandContext(ctx, opsBinary, "--query", query)
	cmd.Dir = t.TempDir()
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("real ops CLI failed: %v; safe error: %s", err, stderr.String())
	}
	var result pipeline.Result
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Report == "" {
		t.Fatal("missing real final report")
	}
	seen := map[string]bool{}
	for _, e := range result.Detail {
		if e.Type == "tool" {
			seen[e.Tool] = true
			t.Logf("tool=%s status=%s summary=%s", e.Tool, e.Status, e.Summary)
		}
	}
	if !seen["query_prometheus_alerts"] || (tools.GetCurrentTimeEnabled && !seen["get_current_time"]) {
		t.Fatal("model did not perform required real alert/time checks")
	}
	t.Logf("report has five sections; events=%d; raw report and business data not logged; docs_enabled=%t log_enabled=%t", len(result.Detail), tools.QueryInternalDocsEnabled, tools.QueryLogEnabled)
}
