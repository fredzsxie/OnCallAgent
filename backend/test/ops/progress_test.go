package ops_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
)

func assertStepPairs(t *testing.T, events []pipeline.Event) {
	t.Helper()
	starts, ends := map[string]pipeline.Event{}, map[string]bool{}
	for i, e := range events {
		if e.Sequence != i+1 {
			t.Fatal("nonconsecutive event", e)
		}
		if e.StepID == "" {
			continue
		}
		if e.Status == "started" {
			if _, ok := starts[e.StepID]; ok {
				t.Fatal("duplicate start", e.StepID)
			}
			starts[e.StepID] = e
			if e.ParentStepID != "" {
				if _, ok := starts[e.ParentStepID]; !ok {
					t.Fatal("missing parent")
				}
			}
		} else {
			start, ok := starts[e.StepID]
			if !ok || ends[e.StepID] || e.DurationMS == nil || e.Title != start.Title {
				t.Fatal("unpaired terminal event", e)
			}
			a, _ := time.Parse(time.RFC3339Nano, e.StartedAt)
			b, _ := time.Parse(time.RFC3339Nano, e.FinishedAt)
			if *e.DurationMS != b.Sub(a).Milliseconds() || *e.DurationMS < 0 {
				t.Fatal("incorrect step duration", e)
			}
			ends[e.StepID] = true
		}
	}
	if len(starts) != len(ends) {
		t.Fatalf("unclosed steps: starts=%d ends=%d", len(starts), len(ends))
	}
}

func TestProgressPairsParallelSameNameToolsAndCopies(t *testing.T) {
	ex := &script{fn: func(_ context.Context, in []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
		if in[len(in)-1].Role == schema.Tool {
			return schema.AssistantMessage("internal model text must not be displayed", nil), nil
		}
		m := call("get_current_time", map[string]any{})
		second := m.ToolCalls[0]
		second.ID = "another-call"
		m.ToolCalls = append(m.ToolCalls, second)
		return m, nil
	}}
	var clock atomic.Int64
	reg := registry(t, false, false)
	a, err := pipeline.BuildOpsAgent(context.Background(), cfg(), pipeline.Dependencies{PlannerModel: plannerModel(), ExecutorModel: ex, Tools: reg.Tools(), Now: func() time.Time { return time.UnixMilli(clock.Add(10)) }})
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.RunWithEvents(context.Background(), "inspect", func(e pipeline.Event) error {
		if e.Output != nil && e.Output.UnixSeconds != nil {
			*e.Output.UnixSeconds = 0
		}
		if e.Output != nil && len(e.Output.Steps) > 0 {
			e.Output.Steps[0] = "changed"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStepPairs(t, r.Detail)
	tools, executes := 0, 0
	for _, e := range r.Detail {
		if e.Type == "execute" && e.Status == "completed" {
			executes++
			if e.Output == nil || len(e.Output.Tools) != 2 {
				t.Fatal("lost tool results")
			}
		}
		if e.Tool == "get_current_time" && e.Status == "completed" {
			tools++
			if e.Output == nil || *e.Output.UnixSeconds == 0 {
				t.Fatal("sink modified stored output")
			}
		}
	}
	if tools != 2 || executes != 1 {
		t.Fatalf("tools=%d executes=%d", tools, executes)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "internal model text") || strings.Contains(string(data), "changed") {
		t.Fatal("internal text or mutable callback data leaked")
	}
}

type resultTool struct{ name, raw string }

func (m resultTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: m.name, ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}
func (m resultTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return m.raw, nil
}

func TestProgressToolDetailsTruncateAndDoNotExposeExtraFields(t *testing.T) {
	for _, name := range []string{"query_prometheus_alerts", "query_internal_docs"} {
		t.Run(name, func(t *testing.T) {
			items := []any{}
			for i := 0; i < 25; i++ {
				if name == "query_prometheus_alerts" {
					items = append(items, map[string]any{"labels": map[string]string{"instance": "node-a", "api_token": "fixture-secret"}, "state": "firing", "annotations": map[string]string{"summary": "CPU high"}, "value": "1"})
				} else {
					items = append(items, map[string]any{"title": "Runbook", "source": "https://user:fixture-secret@docs.example/runbook?token=fixture-secret", "sectionPath": "Diagnosis", "version": "v2", "score": 0.9, "content": strings.Repeat("文", 600)})
				}
			}
			key := "alerts"
			if name == "query_internal_docs" {
				key = "documents"
			}
			raw, _ := json.Marshal(map[string]any{key: items, "source": "prom", "query": "HighCPU", "mode": "hybrid", "private": "fixture-secret"})
			ex := &script{fn: func(_ context.Context, in []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
				if in[len(in)-1].Role == schema.Tool {
					return schema.AssistantMessage("done", nil), nil
				}
				return call(name, map[string]any{"query": "HighCPU"}), nil
			}}
			a, err := pipeline.BuildOpsAgent(context.Background(), cfg(), pipeline.Dependencies{PlannerModel: plannerModel(), ExecutorModel: ex, Tools: []tool.BaseTool{resultTool{name, string(raw)}}})
			if err != nil {
				t.Fatal(err)
			}
			r, err := a.RunWithEvents(context.Background(), "inspect", func(e pipeline.Event) error {
				if e.Output != nil && len(e.Output.Alerts) > 0 {
					e.Output.Alerts[0].Labels["instance"] = "changed"
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			assertStepPairs(t, r.Detail)
			for _, e := range r.Detail {
				if e.Tool != name || e.Status != "completed" {
					continue
				}
				if e.Count == nil || *e.Count != 25 || !e.Output.Truncated {
					t.Fatal("missing full count and truncation")
				}
				if name == "query_prometheus_alerts" {
					if len(e.Output.Alerts) != 20 || e.Output.Alerts[0].Labels["instance"] != "node-a" {
						t.Fatal("bad alert detail or copy")
					}
				} else {
					if len(e.Output.Documents) != 20 || !e.Output.Documents[0].Truncated || len([]rune(e.Output.Documents[0].Excerpt)) > 501 || e.Input.Query != "HighCPU" {
						t.Fatal("bad document detail")
					}
				}
			}
			data, _ := json.Marshal(r.Detail)
			if strings.Contains(string(data), "fixture-secret") {
				t.Fatal("unknown or credential fields leaked")
			}
		})
	}
}

func TestProgressFailureClosesActiveSteps(t *testing.T) {
	ex := &script{fn: func(ctx context.Context, _ []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	c := cfg()
	c.Timeout = 20 * time.Millisecond
	r, err := build(t, c, plannerModel(), ex, nil).Run(context.Background(), "inspect")
	if err == nil {
		t.Fatal("expected timeout")
	}
	assertStepPairs(t, r.Detail)
	for _, e := range r.Detail {
		if e.Type == "execute" && e.Status == "failed" && e.Error == nil {
			t.Fatal("missing safe error")
		}
	}
}
