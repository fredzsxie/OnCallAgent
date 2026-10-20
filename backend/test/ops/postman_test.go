package ops_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
)

func TestPostmanOpsCLI(t *testing.T) {
	base := os.Getenv("ONCALL_OPS_SIM_URL")
	if base == "" {
		t.Skip("set ONCALL_OPS_SIM_URL for local Postman simulation")
	}
	for _, scenario := range []struct{ name, prom, code string }{{"normal", "normal", ""}, {"no-alerts", "empty", ""}, {"replan", "normal", ""}, {"prom-auth", "auth", ""}, {"plan-limit", "normal", "plan_limit"}, {"executor-limit", "normal", "executor_limit"}, {"model-error", "normal", "execution_failed"}} {
		t.Run(scenario.name, func(t *testing.T) {
			env := append(modelEnv(base+"/model"), "ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL="+base+"/prom/"+scenario.prom)
			out, stderr, err := cli(t, env, "--query", "ops:"+scenario.name)
			if scenario.code != "" {
				if err == nil || out != "" || !strings.Contains(stderr, `"code":"`+scenario.code+`"`) {
					t.Fatal(err, out, stderr)
				}
				return
			}
			var result pipeline.Result
			if err != nil || stderr != "" || json.Unmarshal([]byte(out), &result) != nil || result.Report == "" {
				t.Fatal(err, out, stderr)
			}
			if scenario.name == "replan" && !strings.Contains(out, `"type":"replan"`) {
				t.Fatal("no plan adjustment")
			}
			if strings.Contains(out, "fixture-private-secret") || strings.Contains(out, "signature cache expired") {
				t.Fatal("raw upstream result leaked")
			}
		})
	}
}
