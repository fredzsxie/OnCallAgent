package frontend_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/gogf/gf/v2/net/ghttp"
)

type opsFixtureAgent struct {
	mode   *atomic.Value
	agents map[string]*pipeline.Agent
}

func (a opsFixtureAgent) RunWithEvents(ctx context.Context, q string, sink func(pipeline.Event) error) (*pipeline.Result, error) {
	return a.agents[a.mode.Load().(string)].RunWithEvents(ctx, q, sink)
}

// Only the explicit local acceptance helper exposes scenario controls; production routes never do.
func addOpsFixture(t *testing.T, ctx context.Context, server *ghttp.Server, store *testutil.MemoryStore) *aiops.AIOpsService {
	t.Helper()
	base := os.Getenv("ONCALL_OPS_SIM_BASE_URL")
	if base == "" {
		return nil
	}
	r, err := retriever.NewMilvusRetriever(memoryRetriever{store}, config.Retrieval{TopK: 3, ScoreThreshold: -1})
	if err != nil {
		t.Fatal(err)
	}
	mode := &atomic.Value{}
	mode.Store("normal")
	agents := map[string]*pipeline.Agent{}
	for _, name := range []string{"normal", "empty", "limited", "failure", "timeout", "slow", "unsafe"} {
		modelScenario := name
		if name == "limited" {
			modelScenario = "prom-auth"
		}
		if name == "failure" {
			modelScenario = "model-error"
		}
		baseURL := base + "/model/scenario-" + modelScenario
		p, err := client.NewOpenAI(ctx, config.ChatModel{BaseURL: baseURL + "/planner/v1", Model: "planner-id", APIKey: "fixture-only", Timeout: 15 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		e, err := client.NewOpenAI(ctx, config.ChatModel{BaseURL: baseURL + "/executor/v1", Model: "executor-id", APIKey: "fixture-only", Timeout: 15 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		promPath := "normal"
		if name == "empty" {
			promPath = "empty"
		}
		if name == "limited" {
			promPath = "auth"
		}
		prom, err := client.NewPrometheus(config.ToolHTTP{URL: base + "/prom/" + promPath, Timeout: time.Second, Auth: config.HTTPAuth{Type: "none"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(prom.Close)
		reg, err := tools.NewRegistry(config.Tools{TimeZone: "Asia/Shanghai", QueryInternalDocsEnabled: true, GetCurrentTimeEnabled: true, QueryPrometheusAlertsEnabled: true}, tools.Dependencies{Retriever: r, Prometheus: prom})
		if err != nil {
			t.Fatal(err)
		}
		budget := 20 * time.Second
		if name == "timeout" {
			budget = 200 * time.Millisecond
		}
		agents[name], err = pipeline.BuildOpsAgent(ctx, config.OpsAgent{MaxIterations: 3, ExecutorMaxSteps: 8, Timeout: budget}, pipeline.Dependencies{PlannerModel: p.ToolCallingModel(), ExecutorModel: e.ToolCallingModel(), Tools: reg.Tools()})
		if err != nil {
			t.Fatal(err)
		}
	}
	server.BindHandler("/__test/ops-scenario", func(r *ghttp.Request) {
		if r.Method != "POST" {
			r.Response.WriteHeader(405)
			return
		}
		var request struct {
			Scenario string `json:"scenario"`
		}
		if json.Unmarshal(r.GetBody(), &request) != nil || agents[request.Scenario] == nil {
			r.Response.WriteHeader(400)
			return
		}
		mode.Store(request.Scenario)
		r.Response.WriteJson(map[string]string{"scenario": request.Scenario})
	})
	s, err := aiops.New(opsFixtureAgent{mode, agents}, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Store(ctx, []*schema.Document{{ID: "ops-fixture-doc", Content: "HighCPU: check processes and verify load before making changes.", MetaData: map[string]any{"source": "auth.md", "title": "Operations runbook"}}}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	return s
}
