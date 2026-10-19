package operations_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPostmanOperationsCLI(t *testing.T) {
	base := os.Getenv("ONCALL_OPERATIONS_SIM_URL")
	if base == "" {
		t.Skip("set ONCALL_OPERATIONS_SIM_URL to an explicitly started local Postman simulation")
	}
	base = strings.TrimRight(base, "/")
	for _, tc := range []struct {
		name, scenario, tool, query, code string
		list                              bool
		count                             int
	}{
		{"alerts", "default", "query_prometheus_alerts", "", "", false, 2},
		{"alerts_empty", "empty", "query_prometheus_alerts", "", "", false, 0},
		{"alerts_auth", "auth", "query_prometheus_alerts", "", "authentication_failed", false, 0},
		{"alerts_format", "bad", "query_prometheus_alerts", "", "invalid_response", false, 0},
		{"alerts_upstream", "upstream", "query_prometheus_alerts", "", "upstream_error", false, 0},
		{"alerts_timeout", "slow", "query_prometheus_alerts", "", "timeout", false, 0},
		{"mcp_list", "default", "query_log", "", "", true, 1},
		{"logs", "default", "query_log", "level:ERROR", "", false, 1},
		{"logs_empty", "default", "query_log", "__empty__", "", false, 0},
		{"logs_sql", "default", "query_log", "__sql__", "", false, 1},
		{"logs_auth_http", "auth", "query_log", "*", "authentication_failed", false, 0},
		{"logs_auth_provider", "default", "query_log", "__auth__", "authentication_failed", false, 0},
		{"logs_schema", "schema", "query_log", "*", "invalid_response", false, 0},
		{"logs_format", "default", "query_log", "__bad__", "invalid_response", false, 0},
		{"logs_upstream", "default", "query_log", "__upstream__", "upstream_error", false, 0},
		{"logs_timeout", "default", "query_log", "__slow__", "timeout", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := []string{"ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_TOOLS_QUERY_LOG_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL=" + base + "/prom/" + tc.scenario, "ONCALL_LOG_MCP_URL=" + base + "/cls/" + tc.scenario + "/mcp", "ONCALL_LOG_REGION=ap-guangzhou", "ONCALL_LOG_TOPIC_ID=fixture-topic", "ONCALL_CHAT_MODEL_API_KEY=", "ONCALL_EMBEDDING_API_KEY=", "ONCALL_MILVUS_ADDRESS="}
			if strings.Contains(tc.name, "timeout") {
				env = append(env, "ONCALL_PROMETHEUS_TIMEOUT=100ms", "ONCALL_LOG_MCP_TIMEOUT=100ms")
			}
			args := []string{"--tool", tc.tool, "--input", "{}"}
			if tc.tool == "query_log" {
				data, _ := json.Marshal(map[string]any{"query": tc.query, "start_time": "2026-10-19T12:00:00+08:00", "end_time": "2026-10-19T12:15:00+08:00"})
				args[3] = string(data)
			}
			if tc.list {
				args = []string{"--mcp-tools"}
			}
			out, stderr, err := runTool(t, env, args...)
			if tc.code != "" {
				var failure struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err == nil || out != "" || json.Unmarshal([]byte(stderr), &failure) != nil || failure.Error.Code != tc.code || strings.Contains(stderr, "private") {
					t.Fatalf("error classification %s %s %v", out, stderr, err)
				}
				return
			}
			if err != nil || stderr != "" {
				t.Fatalf("CLI call failed %s %s %v", out, stderr, err)
			}
			if tc.list {
				var defs []map[string]any
				if json.Unmarshal([]byte(out), &defs) != nil || len(defs) != 1 || defs[0]["name"] != "SearchLog" {
					t.Fatal("bad MCP list", out)
				}
				return
			}
			var result map[string]json.RawMessage
			if json.Unmarshal([]byte(out), &result) != nil {
				t.Fatal("double encoded result", out)
			}
			field := "alerts"
			if tc.tool == "query_log" {
				field = "records"
			}
			var records []json.RawMessage
			if json.Unmarshal(result[field], &records) != nil || records == nil || len(records) != tc.count {
				t.Fatalf("bad result %s", out)
			}
		})
	}
}
