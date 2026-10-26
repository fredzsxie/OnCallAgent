package recall_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "oncall-recall-cli-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(directory, "recall_cmd")
	build := exec.Command("go", "build", "-o", binary, "./cmd/recall_cmd")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build recall_cmd: %v\n%s", err, output)
		os.RemoveAll(directory)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}

func environment(extra ...string) []string {
	result := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "ONCALL_") && !strings.HasPrefix(value, "ARK_API_KEY=") {
			result = append(result, value)
		}
	}
	return append(result, extra...)
}

func invoke(t *testing.T, extra []string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = environment(extra...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	return out.String(), stderr.String(), err
}

func TestOfflineDefinitionsAndTime(t *testing.T) {
	extra := []string{"ONCALL_EMBEDDING_BASE_URL=", "ONCALL_MILVUS_ADDRESS=", "ONCALL_TIME_ZONE=UTC"}
	output, stderr, err := invoke(t, extra, "--list-tools")
	var definitions []struct{ Name string }
	if err != nil || stderr != "" || json.Unmarshal([]byte(output), &definitions) != nil || len(definitions) != 2 {
		t.Fatalf("offline definitions: %s %s %v", output, stderr, err)
	}
	for _, name := range []string{"query_internal_docs", "get_current_time", "input_schema", "output_schema"} {
		if !strings.Contains(output, name) {
			t.Fatal("missing tool definition:", name)
		}
	}
	before := time.Now().Unix()
	output, stderr, err = invoke(t, extra, "--tool", "get_current_time", "--input", "{}")
	var current struct {
		Timestamp string
		Zone      string `json:"timezone"`
		Seconds   int64  `json:"unix_seconds"`
	}
	if err != nil || stderr != "" || json.Unmarshal([]byte(output), &current) != nil || current.Zone != "UTC" || current.Seconds < before || current.Seconds > time.Now().Unix() {
		t.Fatalf("offline time tool: %s %s %v", output, stderr, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, current.Timestamp); err != nil {
		t.Fatal("invalid timestamp")
	}
	output, stderr, err = invoke(t, []string{"ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED=false", "ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED=false"}, "--list-tools")
	if err != nil || stderr != "" || strings.TrimSpace(output) != "[]" {
		t.Fatalf("disabled definitions: %s %s %v", output, stderr, err)
	}
}

func TestCLIRejectsInvalidModesAndConfig(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--query", " "}, {"--query", "CPU", "--list-tools"}, {"--query", "CPU", "--tool", "get_current_time"},
		{"--tool", "unknown"}, {"--tool", "get_current_time", "--input", "{"}, {"--tool", "get_current_time", "--input", "null"},
		{"--query", "CPU", "--input", "{}"}, {"--query", "CPU", "--top-k", "0"}, {"--query", "CPU", "--score-threshold", "NaN"},
		{"--tool", "get_current_time", "--top-k", "2"}, {"--list-tools", "extra"},
	} {
		output, stderr, err := invoke(t, nil, args...)
		if err == nil || output != "" || strings.TrimSpace(stderr) == "" {
			t.Fatalf("invalid CLI input accepted: %v stdout=%q stderr=%q err=%v", args, output, stderr, err)
		}
	}
	output, stderr, err := invoke(t, []string{"ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED=false"}, "--tool", "get_current_time")
	if err == nil || output != "" || !strings.Contains(stderr, "disabled") {
		t.Fatal("disabled tool could be invoked")
	}
	output, stderr, err = invoke(t, []string{"ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED=false", "ONCALL_EMBEDDING_BASE_URL="}, "--query", "CPU")
	if err == nil || output != "" || !strings.Contains(stderr, "ONCALL_EMBEDDING_BASE_URL") {
		t.Fatal("plain retrieval incorrectly gated by tool switch")
	}
	output, stderr, err = invoke(t, nil, "--help")
	if err != nil || output != "" || !strings.Contains(stderr, "Usage") {
		t.Fatal("help should exit successfully")
	}
}

func TestCLISignalCancelsMilvusConnection(t *testing.T) {
	cmd := exec.Command(binary, "--query", "CPU")
	cmd.Dir = t.TempDir()
	cmd.Env = environment("ONCALL_EMBEDDING_BASE_URL=http://127.0.0.1:1", "ONCALL_EMBEDDING_API_KEY=fixture", "ONCALL_MILVUS_ADDRESS=127.0.0.1:1", "ONCALL_MILVUS_CONNECT_TIMEOUT=30s")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("CLI did not wait for connection: %v", err)
	case <-time.After(time.Second):
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || out.Len() != 0 || !strings.Contains(stderr.String(), "context canceled") {
			t.Fatalf("signal not propagated: %v %s", err, stderr.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("SIGTERM did not cancel connection")
	}
}

func TestCLILogsUserQueryBeforeDependencyInitialization(t *testing.T) {
	query := "  检索问题\n\"quoted\"\t  "
	for _, args := range [][]string{{"--query", query}, {"--tool", "query_internal_docs", "--input", string(mustJSONQuery(t, query))}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = environment("ONCALL_EMBEDDING_BASE_URL=")
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		cancel()
		if err == nil || out.Len() != 0 || !strings.Contains(stderr.String(), "ONCALL_EMBEDDING_BASE_URL") {
			t.Fatal("expected safe configuration failure", err, stderr.String())
		}
		files, _ := filepath.Glob(filepath.Join(cmd.Dir, "log", "oncall-*.log"))
		if len(files) != 1 {
			t.Fatal("missing log")
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(data), "用户检索") != 1 || strings.Count(string(data), "query="+strconv.QuoteToGraphic(query)) != 1 {
			t.Fatal("query missing or duplicated", string(data))
		}
	}
}
func mustJSONQuery(t *testing.T, query string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
