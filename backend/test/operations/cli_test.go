package operations_test

import (
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
	"syscall"
	"testing"
	"time"


)

var toolBinary string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "oncall-tools-cli-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	toolBinary = filepath.Join(directory, "tools_cmd")
	build := exec.Command("go", "build", "-o", toolBinary, "./cmd/tools_cmd")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build tools_cmd: %v\n%s", err, output)
		os.RemoveAll(directory)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}

func toolEnvironment(extra ...string) []string {
	result := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "ONCALL_") && !strings.HasPrefix(value, "ARK_API_KEY=") {
			result = append(result, value)
		}
	}
	return append(result, extra...)
}

func runTool(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, toolBinary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = toolEnvironment(env...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	return out.String(), stderr.String(), err
}

func TestToolsCLIDefinitionsAndTimeNeedNoClients(t *testing.T) {
	env := []string{"ONCALL_CHAT_MODEL_API_KEY=", "ONCALL_EMBEDDING_API_KEY=", "ONCALL_MILVUS_ADDRESS=", "ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL="}
	out, stderr, err := runTool(t, env, "--list-tools")
	var defs []map[string]any
	if err != nil || stderr != "" || json.Unmarshal([]byte(out), &defs) != nil || len(defs) != 3 {
		t.Fatalf("definitions: %s %s %v", out, stderr, err)
	}
	out, stderr, err = runTool(t, env, "--tool", "get_current_time")
	if err != nil || stderr != "" || !json.Valid([]byte(out)) || !strings.Contains(out, "timestamp") {
		t.Fatalf("time-only: %s %s %v", out, stderr, err)
	}
}

func TestToolsCLIActualPrometheusCalls(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, alertsJSON)
	}))
	defer s.Close()
	out, stderr, err := runTool(t, []string{"ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL=" + s.URL, "ONCALL_MILVUS_ADDRESS="}, "--tool", "query_prometheus_alerts", "--input", "{}")
	var alerts map[string]any
	if err != nil || stderr != "" || json.Unmarshal([]byte(out), &alerts) != nil || len(alerts["alerts"].([]any)) != 2 {
		t.Fatalf("prom CLI: %s %s %v", out, stderr, err)
	}
}

func TestToolsCLIRejectsModesAndReportsSafeJSON(t *testing.T) {
	for _, args := range [][]string{{}, {"--list-tools", "--tool", "get_current_time"}, {"--input", "{}"}, {"--tool", "unknown"}, {"--tool", "query_log"}, {"--mcp-tools"}, {"--tool", "get_current_time", "--input", "{"}, {"--bad-flag=fixture-private-secret"}} {
		out, stderr, err := runTool(t, nil, args...)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err == nil || out != "" || json.Unmarshal([]byte(stderr), &body) != nil || body.Error.Code == "" || strings.Contains(stderr, "fixture-private-secret") {
			t.Fatalf("unsafe CLI error args=%v stdout=%q stderr=%q err=%v", args, out, stderr, err)
		}
	}
	out, stderr, err := runTool(t, []string{"ONCALL_TOOLS_QUERY_LOG_ENABLED=true"}, "--tool", "query_log")
	if err == nil || out != "" || !strings.Contains(stderr, `"code":"unknown_tool"`) {
		t.Fatalf("removed tool remained available: %q %q %v", out, stderr, err)
	}
	out, stderr, err = runTool(t, nil, "--help")
	if err != nil || out != "" || !strings.Contains(stderr, "Usage") {
		t.Fatal("help contract failed")
	}
}

func TestToolsCLISignalCancelsPendingQuery(t *testing.T) {
	entered := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	defer s.Close()
	cmd := exec.Command(toolBinary, "--tool", "query_prometheus_alerts")
	cmd.Dir = t.TempDir()
	cmd.Env = toolEnvironment("ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true", "ONCALL_PROMETHEUS_BASE_URL="+s.URL)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("CLI did not start request")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || out.Len() != 0 || !strings.Contains(stderr.String(), `"code":"cancelled"`) {
			t.Fatalf("signal error %v %s", err, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal did not cancel tool")
	}
}

func TestCLILogsUseRunIDsAndKeepJSONStreams(t *testing.T) {
	directory := t.TempDir()
	for range 2 {
		command := exec.Command(toolBinary, "--tool", "get_current_time", "--input", "{}")
		command.Dir = directory
		command.Env = toolEnvironment("ONCALL_LOG_LEVEL=debug")
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		if err := command.Run(); err != nil || !json.Valid(stdout.Bytes()) || stderr.Len() != 0 {
			t.Fatal("CLI protocol changed", err, stdout.String(), stderr.String())
		}
	}
	files, err := filepath.Glob(filepath.Join(directory, "log", "oncall-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatal("missing CLI log", files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "run=") {
				runs[field] = true
			}
		}
	}
	if len(runs) != 2 || !strings.Contains(string(content), "tool=get_current_time") || strings.Contains(string(content), "\x1b") {
		t.Fatal("run correlation or formatting", string(content))
	}
}
