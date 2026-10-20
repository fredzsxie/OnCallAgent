package ops_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	controller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
)

type runFunc func(context.Context, string, func(pipeline.Event) error) (*pipeline.Result, error)

func (f runFunc) RunWithEvents(ctx context.Context, q string, sink func(pipeline.Event) error) (*pipeline.Result, error) {
	return f(ctx, q, sink)
}
func service(t *testing.T, agent aiops.Agent, budget time.Duration) *aiops.AIOpsService {
	t.Helper()
	s, err := aiops.New(agent, budget)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startOpsAPI(t *testing.T, s *aiops.AIOpsService) string {
	t.Helper()
	server := ghttp.GetServer("ops-http-" + guid.S())
	server.SetDumpRouterMap(false)
	server.SetAccessLogEnabled(false)
	server.SetErrorLogEnabled(false)
	server.Group("/api", func(g *ghttp.RouterGroup) {
		g.Middleware(middleware.ResponseMiddleware)
		g.Bind(controller.NewAIOpsV1(s))
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err = server.SetListener(l); err != nil {
		l.Close()
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		l.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Shutdown() })
	return "http://" + l.Addr().String()
}

func TestAIOpsSharedServiceValidationCopiesAndConcurrency(t *testing.T) {
	var count atomic.Int32
	s := service(t, runFunc(func(ctx context.Context, q string, sink func(pipeline.Event) error) (*pipeline.Result, error) {
		count.Add(1)
		n := 0
		e := pipeline.Event{Sequence: 1, Type: "tool", Status: "completed", Count: &n}
		if sink != nil {
			sink(e)
		}
		return &pipeline.Result{Report: q, Detail: []pipeline.Event{e}}, nil
	}), time.Second)
	out, err := s.Analyze(context.Background())
	if err != nil || out.Report != aiops.DefaultQuery {
		t.Fatal(err)
	}
	if _, err = s.RunWithEvents(context.Background(), "  ", nil); !errors.Is(err, pipeline.ErrInvalidInput) || count.Load() != 1 {
		t.Fatal("empty query reached agent")
	}
	var event pipeline.Event
	out, err = s.RunWithEvents(context.Background(), " query ", func(e pipeline.Event) error { event = e; return nil })
	if err != nil || out.Report != "query" {
		t.Fatal(err)
	}
	*event.Count = 99
	if *out.Detail[0].Count != 0 {
		t.Fatal("result exposes agent data")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Analyze(context.Background())
			if err != nil || r.Report != aiops.DefaultQuery {
				t.Error(err)
			}
			*r.Detail[0].Count = 5
		}()
	}
	wg.Wait()
	if _, err = aiops.New(nil, time.Second); !errors.Is(err, aiops.ErrInitialization) {
		t.Fatal(err)
	}
}

func TestAIOpsActualGraphHTTPContractAndCounts(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		empty, fail bool
	}{{"normal", false, false}, {"empty", true, false}, {"log-failure", false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			a := build(t, cfg(), plannerModel(), toolExecutor(t, scenario.empty, scenario.fail), registry(t, scenario.empty, scenario.fail))
			url := startOpsAPI(t, service(t, a, time.Second))
			response, err := http.Post(url+"/api/ai_ops", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var body struct {
				Message string `json:"message"`
				Data    struct {
					Result string           `json:"result"`
					Detail []pipeline.Event `json:"detail"`
				} `json:"data"`
			}
			if json.NewDecoder(response.Body).Decode(&body) != nil || response.StatusCode != 200 || body.Message != "OK" || body.Data.Result == "" {
				t.Fatalf("bad response %+v", body)
			}
			seen := false
			for _, e := range body.Data.Detail {
				if e.Tool == "query_prometheus_alerts" {
					seen = true
					if e.Count == nil || (*e.Count == 0) != scenario.empty {
						t.Fatal("missing typed count", e)
					}
				}
			}
			if !seen {
				t.Fatal("graph not executed")
			}
		})
	}
}

func TestAIOpsErrorsPreservePartialDetailAndHTTPStatus(t *testing.T) {
	for _, item := range []struct {
		name   string
		err    error
		status int
		code   string
	}{{"input", pipeline.ErrInvalidInput, 400, "invalid_input"}, {"cancel", context.Canceled, 408, "cancelled"}, {"timeout", context.DeadlineExceeded, 504, "timeout"}, {"model", errors.New("provider-private-secret"), 502, "execution_failed"}, {"plan", pipeline.ErrPlanLimit, 502, "plan_limit"}, {"executor", pipeline.ErrExecutorLimit, 502, "executor_limit"}, {"initialization", aiops.ErrInitialization, 500, "initialization_failed"}, {"internal", aiops.ErrInternal, 500, "internal_error"}, {"event-write", pipeline.ErrEventWrite, 500, "event_write_failed"}, {"empty", nil, 502, "invalid_report"}} {
		t.Run(item.name, func(t *testing.T) {
			s := service(t, runFunc(func(context.Context, string, func(pipeline.Event) error) (*pipeline.Result, error) {
				return &pipeline.Result{Detail: []pipeline.Event{{Sequence: 1, Summary: "partial evidence"}}}, item.err
			}), time.Second)
			response, err := http.Post(startOpsAPI(t, s)+"/api/ai_ops", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)
			var body struct {
				Data struct {
					Code   string           `json:"code"`
					Detail []pipeline.Event `json:"detail"`
					Result string           `json:"result"`
				} `json:"data"`
			}
			if response.StatusCode != item.status || json.Unmarshal(data, &body) != nil || body.Data.Code != item.code || len(body.Data.Detail) != 1 || body.Data.Result != "" || strings.Contains(string(data), "provider-private-secret") {
				t.Fatalf("%d %s", response.StatusCode, data)
			}
		})
	}
}

func TestAIOpsMalformedBodyAndDisconnect(t *testing.T) {
	var called atomic.Int32
	s := service(t, runFunc(func(context.Context, string, func(pipeline.Event) error) (*pipeline.Result, error) {
		called.Add(1)
		return &pipeline.Result{Report: "report"}, nil
	}), time.Second)
	response, err := http.Post(startOpsAPI(t, s)+"/api/ai_ops", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 400 || called.Load() != 0 {
		t.Fatalf("malformed body: status=%d calls=%d body=%s", response.StatusCode, called.Load(), data)
	}
	entered, done := make(chan struct{}), make(chan struct{})
	s = service(t, runFunc(func(ctx context.Context, _ string, _ func(pipeline.Event) error) (*pipeline.Result, error) {
		close(entered)
		<-ctx.Done()
		close(done)
		return nil, ctx.Err()
	}), time.Second)
	url := startOpsAPI(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url+"/api/ai_ops", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	go func() {
		r, _ := http.DefaultClient.Do(req)
		if r != nil {
			r.Body.Close()
		}
	}()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("disconnect not propagated")
	}
}
