package ops_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

var opsBinary, stdioBinary string

func TestSDKPlanningToolTurn(t *testing.T) {
	s, _, _ := modelServer(t, "normal")
	defer s.Close()
	c, err := client.NewOpenAI(context.Background(), config.ChatModel{BaseURL: s.URL + "/planner/v1", Model: "planner-id", APIKey: "planner-key", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, err := c.ToolCallingModel().WithTools([]*schema.ToolInfo{{Name: "Plan", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"steps": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String}, Required: true}})}})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("inspect")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := schema.ConcatMessageStream(sr)
	if err != nil || msg == nil || len(msg.ToolCalls) != 1 {
		t.Fatalf("planning turn: msg=%+v err=%v", msg, err)
	}
	var p map[string]any
	if json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &p) != nil {
		t.Fatal("invalid split arguments", msg.ToolCalls[0].Function.Arguments)
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "oncall-ops-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opsBinary = filepath.Join(dir, "ai_ops_cmd")
	stdioBinary = filepath.Join(dir, "stdio_fixture")
	for _, item := range []struct{ binary, pkg string }{{opsBinary, "./cmd/ai_ops_cmd"}, {stdioBinary, "./test/operations/stdiofixture"}} {
		cmd := exec.Command("go", "build", "-o", item.binary, item.pkg)
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, output)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func cleanEnv(extra ...string) []string {
	values := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "ONCALL_") && !strings.HasPrefix(v, "ARK_API_KEY=") && !strings.HasPrefix(v, "TENCENTCLOUD_") {
			values = append(values, v)
		}
	}
	return append(values, extra...)
}

func cli(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, opsBinary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = cleanEnv(env...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	return out.String(), stderr.String(), err
}

type modelRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role      schema.RoleType   `json:"role"`
		Name      string            `json:"name"`
		ToolCalls []schema.ToolCall `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice"`
}

func respondModel(w http.ResponseWriter, req modelRequest, msg *schema.Message) {
	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "object": "chat.completion", "model": req.Model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if len(msg.ToolCalls) > 0 {
		c := msg.ToolCalls[0]
		args := c.Function.Arguments
		cut := len(args) / 2
		for i, part := range []string{args[:cut], args[cut:]} {
			function := map[string]any{"arguments": part}
			deltaCall := map[string]any{"index": 0, "function": function}
			if i == 0 {
				function["name"] = c.Function.Name
				deltaCall["id"] = "stream-call"
				deltaCall["type"] = "function"
			}
			data, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{deltaCall}}}}})
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	} else {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": msg.Content}}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func modelServer(t *testing.T, scenario string) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	plannerCalls, executorCalls, reviews := &atomic.Int32{}, &atomic.Int32{}, &atomic.Int32{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req modelRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid model request")
			w.WriteHeader(400)
			return
		}
		if scenario == "cancel" {
			<-r.Context().Done()
			return
		}
		if scenario == "model-error" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			io.WriteString(w, `{"error":{"message":"fixture-private-secret"}}`)
			return
		}
		var msg *schema.Message
		if strings.HasPrefix(r.URL.Path, "/planner/") {
			plannerCalls.Add(1)
			if req.Model != "planner-id" || r.Header.Get("Authorization") != "Bearer planner-key" {
				t.Error("planner config was not isolated")
			}
			if len(req.ToolChoice) == 0 {
				t.Error("planning tool choice missing")
			}
			if len(req.Tools) == 1 {
				msg = planCall()
			} else if scenario == "plan-limit" || (scenario == "replan" && reviews.Add(1) == 1) {
				msg = planCall()
			} else {
				msg = reportCall()
			}
		} else {
			executorCalls.Add(1)
			if req.Model != "executor-id" || r.Header.Get("Authorization") != "Bearer executor-key" {
				t.Error("executor config was not isolated")
			}
			if scenario == "executor-limit" {
				msg = call("get_current_time", map[string]any{})
			} else {
				for _, name := range []string{"query_prometheus_alerts", "get_current_time", "query_log"} {
					enabled := false
					for _, tool := range req.Tools {
						if tool.Function.Name == name {
							enabled = true
						}
					}
					seen := false
					for _, m := range req.Messages {
						for _, c := range m.ToolCalls {
							if c.Function.Name == name {
								seen = true
							}
						}
					}
					if enabled && !seen {
						args := map[string]any{}
						if name == "query_log" {
							args["query"] = "level:ERROR"
						}
						msg = call(name, args)
						break
					}
				}
				if msg == nil {
					msg = schema.AssistantMessage("Available evidence collected; retain tool errors as unconfirmed.", nil)
				}
			}
		}
		respondModel(w, req, msg)
	}))
	return s, plannerCalls, executorCalls
}

func modelEnv(base string) []string {
	return []string{
		"ONCALL_OPS_PLANNER_BASE_URL=" + base + "/planner/v1", "ONCALL_OPS_PLANNER_MODEL=planner-id", "ONCALL_OPS_PLANNER_API_KEY=planner-key",
		"ONCALL_OPS_EXECUTOR_BASE_URL=" + base + "/executor/v1", "ONCALL_OPS_EXECUTOR_MODEL=executor-id", "ONCALL_OPS_EXECUTOR_API_KEY=executor-key",
		"ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED=false", "ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=false", "ONCALL_TOOLS_QUERY_LOG_ENABLED=false",
		"ONCALL_OPS_MAX_ITERATIONS=2", "ONCALL_OPS_EXECUTOR_MAX_STEPS=6", "ONCALL_OPS_TIMEOUT=5s",
	}
}

func TestCLISDKReportEventsAndOperations(t *testing.T) {
	for _, scenario := range []string{"normal", "no-alerts", "log-failure", "replan"} {
		t.Run(scenario, func(t *testing.T) {
			s, pc, ec := modelServer(t, scenario)
			defer s.Close()
			var promCalls atomic.Int32
			prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				promCalls.Add(1)
				if r.URL.Path != "/api/v1/alerts" {
					t.Error(r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				alerts := []any{}
				if scenario != "no-alerts" {
					alerts = append(alerts, map[string]any{"labels": map[string]string{"alertname": "HighCPU", "instance": "node-a"}, "annotations": map[string]string{}, "state": "firing", "activeAt": "2026-10-19T00:00:00Z", "value": "1"})
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"alerts": alerts}})
			}))
			defer prom.Close()
			env := modelEnv(s.URL)
			env = append(env, "ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL="+prom.URL, "ONCALL_TOOLS_QUERY_LOG_ENABLED=true", "ONCALL_LOG_MCP_COMMAND="+stdioBinary, "ONCALL_LOG_REGION=ap-guangzhou", "ONCALL_LOG_TOPIC_ID=test-topic", "TENCENTCLOUD_SECRET_ID=fixture-id", "TENCENTCLOUD_SECRET_KEY=fixture-key")
			args := `["--ops-log"]`
			if scenario == "log-failure" {
				args = `["--ops-log-fail"]`
			}
			env = append(env, "ONCALL_LOG_MCP_ARGS_JSON="+args)
			path := filepath.Join(t.TempDir(), "events.jsonl")
			out, stderr, err := cli(t, env, "--query", "investigate", "--events", path)
			var result pipeline.Result
			if err != nil || stderr != "" || json.Unmarshal([]byte(out), &result) != nil || result.Report == "" {
				t.Fatalf("%v %s %s", err, stderr, out)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(bytes.NewReader(data))
			count := 0
			for scanner.Scan() {
				var e pipeline.Event
				if json.Unmarshal(scanner.Bytes(), &e) != nil {
					t.Fatal("invalid event JSONL")
				}
				count++
				if e.Sequence != count {
					t.Fatal(e)
				}
			}
			if count != len(result.Detail) || pc.Load() < 2 || ec.Load() < 2 || promCalls.Load() == 0 {
				t.Fatal("path not executed", pc.Load(), ec.Load(), promCalls.Load(), count)
			}
			if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
				t.Fatal("event file permissions")
			}
			if strings.Contains(string(data), "signature cache expired") || strings.Contains(out, "fixture-private-secret") || strings.Contains(string(data), "fixture-key") {
				t.Fatal("raw log/credential leaked")
			}
			if scenario == "log-failure" && !strings.Contains(string(data), "authentication_failed") {
				t.Fatal("missing recoverable failure")
			}
			if scenario == "replan" && !strings.Contains(string(data), `"type":"replan"`) {
				t.Fatal("missing adjustment")
			}
		})
	}
}

func TestCLIInvalidArgumentsLimitsAndFailures(t *testing.T) {
	for _, args := range [][]string{{}, {"--query", " "}, {"--unknown=private-value"}, {"--query", "inspect", "extra"}} {
		out, stderr, err := cli(t, nil, args...)
		if err == nil || out != "" || !json.Valid([]byte(stderr)) || strings.Contains(stderr, "private-value") {
			t.Fatal(out, stderr, err)
		}
	}
	for _, scenario := range []string{"plan-limit", "executor-limit", "model-error", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			mode := scenario
			if mode == "timeout" {
				mode = "cancel"
			}
			s, _, _ := modelServer(t, mode)
			defer s.Close()
			env := modelEnv(s.URL)
			if scenario == "timeout" {
				env = append(env, "ONCALL_OPS_TIMEOUT=100ms")
			}
			out, stderr, err := cli(t, env, "--query", "inspect")
			if err == nil || out != "" || !json.Valid([]byte(stderr)) || strings.Contains(stderr, "fixture-private-secret") {
				t.Fatal(err, out, stderr)
			}
			code := strings.ReplaceAll(scenario, "-", "_")
			if scenario == "model-error" {
				code = "execution_failed"
			}
			if !strings.Contains(stderr, `"code":"`+code+`"`) {
				t.Fatal(stderr)
			}
		})
	}
	out, stderr, err := cli(t, nil, "--help")
	if err != nil || out != "" || !strings.Contains(stderr, "query") {
		t.Fatal(out, stderr, err)
	}
}

func TestCLISIGTERM(t *testing.T) {
	entered := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, opsBinary, "--query", "inspect")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanEnv(modelEnv(s.URL)...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("model not entered")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if cmd.Wait() == nil || out.Len() != 0 || !strings.Contains(stderr.String(), `"code":"cancelled"`) {
		t.Fatal(out.String(), stderr.String())
	}
}
