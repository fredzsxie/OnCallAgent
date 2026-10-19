package ops_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

type script struct {
	fn     func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error)
	stream func(context.Context) (*schema.StreamReader[*schema.Message], error)
	infos  []*schema.ToolInfo
}

func (m *script) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	clone.infos = infos
	return &clone, nil
}
func (m *script) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.fn(ctx, in, m.infos)
}
func (m *script) Stream(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.stream != nil {
		return m.stream(ctx)
	}
	msg, err := m.fn(ctx, in, m.infos)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func call(name string, value any) *schema.Message {
	data, _ := json.Marshal(value)
	i := 0
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &i, ID: "call-0", Type: "function", Function: schema.FunctionCall{Name: name, Arguments: string(data)}}}}
}
func planCall() *schema.Message {
	return call("Plan", map[string]any{"steps": []string{"Inspect alerts and evidence"}})
}
func reportCall() *schema.Message {
	return call("Respond", map[string]string{"alerts": "HighCPU on node-a", "evidence": "auth.md; time and logs were checked where available", "root_causes": "Candidate only: stale key cache", "recommendations": "Follow auth.md; do not change production automatically", "unconfirmed": "Missing or failed tools remain unconfirmed; no alerts does not prove health"})
}
func plannerModel() *script {
	return &script{fn: func(_ context.Context, _ []*schema.Message, infos []*schema.ToolInfo) (*schema.Message, error) {
		if len(infos) == 1 {
			return planCall(), nil
		}
		return reportCall(), nil
	}}
}
func cfg() config.OpsAgent {
	return config.OpsAgent{MaxIterations: 8, ExecutorMaxSteps: 12, Timeout: 3 * time.Second}
}
func build(t *testing.T, c config.OpsAgent, planner, executor model.ToolCallingChatModel, registry *tools.Registry) *pipeline.Agent {
	t.Helper()
	deps := pipeline.Dependencies{PlannerModel: planner, ExecutorModel: executor}
	if registry != nil {
		deps.Tools = registry.Tools()
	}
	a, err := pipeline.BuildOpsAgent(context.Background(), c, deps)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type alertFunc func(context.Context) (operations.AlertsResult, error)

func (f alertFunc) Alerts(ctx context.Context) (operations.AlertsResult, error) { return f(ctx) }

type logFunc func(context.Context, *operations.LogInput) (operations.LogResult, error)

func (f logFunc) Query(ctx context.Context, in *operations.LogInput) (operations.LogResult, error) {
	return f(ctx, in)
}

type recallFunc func(context.Context, string) ([]*schema.Document, error)

func (f recallFunc) Retrieve(ctx context.Context, q string, _ ...einoretriever.Option) ([]*schema.Document, error) {
	return f(ctx, q)
}

func registry(t *testing.T, empty, logFails bool) *tools.Registry {
	t.Helper()
	r, err := retriever.NewMilvusRetriever(recallFunc(func(context.Context, string) ([]*schema.Document, error) {
		if empty {
			return []*schema.Document{}, nil
		}
		return []*schema.Document{(&schema.Document{ID: "chunk-1", Content: "Refresh signing keys.", MetaData: map[string]any{"source": "auth.md", "title": "Recovery"}}).WithScore(.9)}, nil
	}), config.Retrieval{TopK: 3, ScoreThreshold: .5})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryInternalDocsEnabled: true, GetCurrentTimeEnabled: true, QueryPrometheusAlertsEnabled: true, QueryLogEnabled: true}, tools.Dependencies{Retriever: r,
		Now: func() time.Time { return time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) },
		Prometheus: alertFunc(func(context.Context) (operations.AlertsResult, error) {
			alerts := []operations.Alert{}
			if !empty {
				alerts = append(alerts, operations.Alert{Labels: map[string]string{"alertname": "HighCPU", "instance": "node-a"}})
			}
			return operations.AlertsResult{Source: "prom", Alerts: alerts}, nil
		}),
		Logs: logFunc(func(context.Context, *operations.LogInput) (operations.LogResult, error) {
			if logFails {
				return operations.LogResult{}, operations.Failure(operations.LogTool, operations.ErrAuthentication)
			}
			return operations.LogResult{Records: []json.RawMessage{json.RawMessage(`{"Source":"node-a","message":"private-log-body"}`)}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func toolExecutor(t *testing.T, empty, logFails bool) *script {
	return &script{fn: func(_ context.Context, in []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
		last := in[len(in)-1]
		for _, request := range []struct{ name, args string }{{"query_prometheus_alerts", "{}"}, {"get_current_time", "{}"}, {"query_internal_docs", `{"query":"HighCPU node-a"}`}, {"query_log", `{"query":"level:ERROR","region":"ap-guangzhou","topic_id":"test-topic"}`}} {
			found := false
			for _, m := range in {
				if m.Role == schema.Tool && m.ToolName == request.name {
					found = true
					if request.name == "query_log" && logFails && !strings.Contains(m.Content, "authentication_failed") {
						t.Error("log failure did not reach executor")
					}
					if request.name == "query_internal_docs" && !empty && !strings.Contains(m.Content, "auth.md") {
						t.Error("lost evidence source")
					}
				}
			}
			if !found {
				msg := call(request.name, map[string]any{})
				msg.ToolCalls[0].Function.Arguments = request.args
				return msg, nil
			}
		}
		if last.Role != schema.Tool {
			t.Error("missing tool results")
		}
		return schema.AssistantMessage("Evidence gathered; include disabled or failed tools as limitations.", nil), nil
	}}
}

func TestActualADKAnalysisAndRecoverableToolFailures(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		empty, failed bool
	}{{"normal", false, false}, {"no-alerts-and-no-runbook", true, false}, {"log-failed", false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			a := build(t, cfg(), plannerModel(), toolExecutor(t, scenario.empty, scenario.failed), registry(t, scenario.empty, scenario.failed))
			var events []pipeline.Event
			r, err := a.RunWithEvents(context.Background(), "Investigate alerts", func(e pipeline.Event) error { events = append(events, e); return nil })
			if err != nil {
				t.Fatal(err)
			}
			for _, heading := range []string{"## 告警", "## 证据", "## 根因候选", "## 处理建议", "## 未确认事项"} {
				if !strings.Contains(r.Report, heading) {
					t.Fatal(r.Report)
				}
			}
			toolCount := 0
			for i, e := range r.Detail {
				if e.Sequence != i+1 || e.Timestamp == "" {
					t.Fatal(e)
				}
				if e.Type == "tool" {
					toolCount++
					if e.Tool == "query_log" && scenario.failed && e.Status != "failed" {
						t.Fatal(e)
					}
					if scenario.empty && (e.Tool == "query_internal_docs" || e.Tool == "query_prometheus_alerts") && !strings.HasSuffix(e.Summary, "=0") {
						t.Fatal(e)
					}
				}
			}
			data, _ := json.Marshal(r.Detail)
			if toolCount != 4 || len(events) != len(r.Detail) || strings.Contains(string(data), "private-log-body") {
				t.Fatalf("details %s", data)
			}
		})
	}
}

func TestReplanAndBothExecutionLimits(t *testing.T) {
	for _, scenario := range []string{"adjust", "plan-limit", "executor-limit"} {
		t.Run(scenario, func(t *testing.T) {
			var reviews atomic.Int32
			p := &script{fn: func(_ context.Context, _ []*schema.Message, infos []*schema.ToolInfo) (*schema.Message, error) {
				if len(infos) == 1 {
					return planCall(), nil
				}
				if scenario == "plan-limit" || reviews.Add(1) == 1 {
					return planCall(), nil
				}
				return reportCall(), nil
			}}
			ex := &script{fn: func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
				if scenario == "executor-limit" {
					return call("get_current_time", map[string]any{}), nil
				}
				return schema.AssistantMessage("checked", nil), nil
			}}
			c := cfg()
			c.MaxIterations = 2
			c.ExecutorMaxSteps = 2
			r, err := build(t, c, p, ex, registry(t, false, false)).Run(context.Background(), scenario)
			switch scenario {
			case "adjust":
				if err != nil {
					t.Fatal(err)
				}
			case "plan-limit":
				if !errors.Is(err, pipeline.ErrPlanLimit) {
					t.Fatal(err)
				}
			case "executor-limit":
				if !errors.Is(err, pipeline.ErrExecutorLimit) {
					t.Fatal(err)
				}
			}
			if r == nil || len(r.Detail) == 0 {
				t.Fatal("lost partial details")
			}
			if err != nil && r.Report != "" {
				t.Fatal("error returned a report")
			}
		})
	}
}

func TestInvalidPlansReportsModelErrorsAndSinkFailure(t *testing.T) {
	for _, scenario := range []string{"model", "empty-plan", "missing-call", "invalid-report", "multiple-calls", "stream-error", "sink"} {
		t.Run(scenario, func(t *testing.T) {
			p := plannerModel()
			switch scenario {
			case "model":
				p.fn = func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
					return nil, errors.New("provider-private-secret")
				}
			case "empty-plan":
				p.fn = func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
					return call("Plan", map[string]any{"steps": []string{}}), nil
				}
			case "missing-call":
				p.fn = func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
					return schema.AssistantMessage("not a plan", nil), nil
				}
			case "invalid-report":
				p.fn = func(_ context.Context, _ []*schema.Message, infos []*schema.ToolInfo) (*schema.Message, error) {
					if len(infos) == 1 {
						return planCall(), nil
					}
					return call("Respond", map[string]string{"alerts": "only one section"}), nil
				}
			case "multiple-calls":
				p.fn = func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
					m := planCall()
					m.ToolCalls = append(m.ToolCalls, m.ToolCalls[0])
					return m, nil
				}
			case "stream-error":
				p.stream = func(context.Context) (*schema.StreamReader[*schema.Message], error) {
					r, w := schema.Pipe[*schema.Message](1)
					w.Send(nil, errors.New("stream-private-secret"))
					w.Close()
					return r, nil
				}
			}
			ex := &script{fn: func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
				return schema.AssistantMessage("checked", nil), nil
			}}
			a := build(t, cfg(), p, ex, nil)
			sink := func(pipeline.Event) error { return nil }
			if scenario == "sink" {
				sink = func(pipeline.Event) error { return errors.New("file-private-path") }
			}
			r, err := a.RunWithEvents(context.Background(), "inspect", sink)
			if err == nil || strings.Contains(err.Error(), "private") || r.Report != "" {
				t.Fatalf("result=%+v err=%v", r, err)
			}
			if scenario == "invalid-report" && !errors.Is(err, pipeline.ErrInvalidReport) {
				t.Fatal(err)
			}
			if scenario == "sink" && !errors.Is(err, pipeline.ErrEventWrite) {
				t.Fatal(err)
			}
		})
	}
}

func TestCancellationTimeoutAndConcurrentStateIsolation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			entered := make(chan struct{}, 1)
			ex := &script{fn: func(ctx context.Context, _ []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			c := cfg()
			c.Timeout = 100 * time.Millisecond
			a := build(t, c, plannerModel(), ex, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				go func() { <-entered; cancel() }()
			}
			r, err := a.Run(ctx, "inspect")
			expected := context.DeadlineExceeded
			if cancelled {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) || len(r.Detail) == 0 {
				t.Fatal(err, r)
			}
		})
	}
	ex := &script{fn: func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
		return schema.AssistantMessage("checked", nil), nil
	}}
	a := build(t, cfg(), plannerModel(), ex, nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := a.Run(context.Background(), "concurrent")
			if err != nil {
				t.Error(err)
				return
			}
			if len(r.Detail) != 4 || r.Detail[1].Iteration != 1 || r.Detail[0].Sequence != 1 {
				t.Errorf("shared state: %+v", r.Detail)
			}
		}()
	}
	wg.Wait()
	if _, err := a.Run(context.Background(), "  "); !errors.Is(err, pipeline.ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestToolTimeoutFormatAndUpstreamErrorsAreRecoverable(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, operations.ErrInvalidResponse, operations.ErrUpstream} {
		t.Run(failure.Error(), func(t *testing.T) {
			reg, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryLogEnabled: true}, tools.Dependencies{Logs: logFunc(func(ctx context.Context, _ *operations.LogInput) (operations.LogResult, error) {
				if errors.Is(failure, context.DeadlineExceeded) {
					child, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
					defer cancel()
					<-child.Done()
					return operations.LogResult{}, child.Err()
				}
				return operations.LogResult{}, failure
			})})
			if err != nil {
				t.Fatal(err)
			}
			ex := &script{fn: func(_ context.Context, in []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
				last := in[len(in)-1]
				if last.Role != schema.Tool {
					return call("query_log", map[string]string{"query": "*"}), nil
				}
				if !strings.Contains(last.Content, operations.Failure("query_log", failure).Code) {
					t.Error("lost safe error", last.Content)
				}
				return schema.AssistantMessage("Continue with available evidence; logs unavailable.", nil), nil
			}}
			result, err := build(t, cfg(), plannerModel(), ex, reg).Run(context.Background(), "inspect")
			if err != nil || result.Report == "" {
				t.Fatal(err)
			}
		})
	}
}

func TestParentDeadlineAndBoundedEventSummaries(t *testing.T) {
	ex := &script{fn: func(ctx context.Context, _ []*schema.Message, _ []*schema.ToolInfo) (*schema.Message, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	a := build(t, cfg(), plannerModel(), ex, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := a.Run(ctx, "inspect")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("parent deadline was not preserved", err)
	}
	p := plannerModel()
	p.fn = func(_ context.Context, _ []*schema.Message, infos []*schema.ToolInfo) (*schema.Message, error) {
		if len(infos) == 1 {
			return call("Plan", map[string]any{"steps": []string{strings.Repeat("测", 3000)}}), nil
		}
		return reportCall(), nil
	}
	ex = &script{fn: func(context.Context, []*schema.Message, []*schema.ToolInfo) (*schema.Message, error) {
		return schema.AssistantMessage("checked", nil), nil
	}}
	r, err := build(t, cfg(), p, ex, nil).Run(context.Background(), "inspect")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Detail {
		if len([]rune(e.Summary)) > 1025 {
			t.Fatal("unbounded summary")
		}
	}
}
