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
		name, scenario, code string
		count                int
	}{
		{"alerts", "default", "", 2},
		{"alerts_empty", "empty", "", 0},
		{"alerts_auth", "auth", "authentication_failed", 0},
		{"alerts_format", "bad", "invalid_response", 0},
		{"alerts_upstream", "upstream", "upstream_error", 0},
		{"alerts_timeout", "slow", "timeout", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := []string{"ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL=" + base + "/prom/" + tc.scenario, "ONCALL_CHAT_MODEL_API_KEY=", "ONCALL_EMBEDDING_API_KEY=", "ONCALL_MILVUS_ADDRESS="}
			if strings.Contains(tc.name, "timeout") {
				env = append(env, "ONCALL_PROMETHEUS_TIMEOUT=100ms")
			}
			out, stderr, err := runTool(t, env, "--tool", "query_prometheus_alerts", "--input", "{}")
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
			var result map[string]json.RawMessage
			if json.Unmarshal([]byte(out), &result) != nil {
				t.Fatal("double encoded result", out)
			}
			var records []json.RawMessage
			if json.Unmarshal(result["alerts"], &records) != nil || records == nil || len(records) != tc.count {
				t.Fatalf("bad result %s", out)
			}
		})
	}
}
