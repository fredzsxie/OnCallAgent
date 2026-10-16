package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"google.golang.org/grpc"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "oncall-chat-cli-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "chat_cmd")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/chat_cmd")
	cmd.Dir = "../../.."
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v %s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type milvusFixture struct {
	milvuspb.UnimplementedMilvusServiceServer
}

func (*milvusFixture) Connect(context.Context, *milvuspb.ConnectRequest) (*milvuspb.ConnectResponse, error) {
	return &milvuspb.ConnectResponse{Status: &commonpb.Status{}, Identifier: 1, ServerInfo: &commonpb.ServerInfo{BuildTags: "v2.4.1"}}, nil
}
func (*milvusFixture) HasCollection(context.Context, *milvuspb.HasCollectionRequest) (*milvuspb.BoolResponse, error) {
	return &milvuspb.BoolResponse{Status: &commonpb.Status{}, Value: true}, nil
}
func (*milvusFixture) DescribeCollection(_ context.Context, r *milvuspb.DescribeCollectionRequest) (*milvuspb.DescribeCollectionResponse, error) {
	return &milvuspb.DescribeCollectionResponse{Status: &commonpb.Status{}, Schema: knowledge.Schema(r.CollectionName, 3).ProtoMessage(), CollectionID: 1, ConsistencyLevel: commonpb.ConsistencyLevel_Strong}, nil
}
func (*milvusFixture) DescribeIndex(context.Context, *milvuspb.DescribeIndexRequest) (*milvuspb.DescribeIndexResponse, error) {
	return &milvuspb.DescribeIndexResponse{Status: &commonpb.Status{}, IndexDescriptions: []*milvuspb.IndexDescription{{FieldName: "vector", IndexName: "vector_idx", Params: []*commonpb.KeyValuePair{{Key: "index_type", Value: "HNSW"}, {Key: "metric_type", Value: "COSINE"}}}}}, nil
}
func (*milvusFixture) Search(context.Context, *milvuspb.SearchRequest) (*milvuspb.SearchResults, error) {
	return &milvuspb.SearchResults{Status: &commonpb.Status{}, Results: &schemapb.SearchResultData{NumQueries: 1, TopK: 1, Topks: []int64{1}, Scores: []float32{0.9}, Ids: &schemapb.IDs{IdField: &schemapb.IDs_StrId{StrId: &schemapb.StringArray{Data: []string{"chunk-1"}}}}, FieldsData: []*schemapb.FieldData{entity.NewColumnVarChar("content", []string{"Refresh signing keys."}).FieldData(), entity.NewColumnJSONBytes("metadata", [][]byte{[]byte(`{"source":"auth.md","title":"Recovery"}`)}).FieldData()}}}, nil
}

func fixtures(t *testing.T) []string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	milvuspb.RegisterMilvusServiceServer(grpcServer, &milvusFixture{})
	go grpcServer.Serve(listener)
	t.Cleanup(grpcServer.Stop)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-only" {
			t.Error("missing fixture authentication")
		}
		if r.URL.Path == "/api/v3/embeddings/multimodal" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":{"embedding":[1,2,3]}}`)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unsupported fixture endpoint", 501)
			return
		}
		var body struct {
			Messages []*schema.Message
			Stream   bool
			Tools    []json.RawMessage
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) == 0 {
			http.Error(w, "bad request", 400)
			return
		}
		if len(body.Tools) != 2 {
			t.Error("registered tools not bound to model")
		}
		last := body.Messages[len(body.Messages)-1]
		if last.Content == "failure" {
			http.Error(w, `{"error":{"message":"fixture-only","type":"server_error"}}`, 500)
			return
		}
		if last.Content == "wait" {
			<-r.Context().Done()
			return
		}
		if !strings.Contains(body.Messages[0].Content, "auth.md") {
			t.Error("retrieved evidence missing from actual HTTP request")
		}
		answer := schema.AssistantMessage("Refresh keys [auth.md]", nil)
		if last.Content == "tool" {
			answer = &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: intPointer(0), ID: "call-time", Type: "function", Function: schema.FunctionCall{Name: "get_current_time", Arguments: "{}"}}}}
		}
		if last.Role == schema.Tool {
			if !strings.Contains(last.Content, "Asia/Shanghai") {
				t.Error("missing time result")
			}
			answer = schema.AssistantMessage("Time tool returned Asia/Shanghai", nil)
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			deltas := []*schema.Message{{Role: schema.Assistant, Content: "Refresh "}, {Content: "keys [auth.md]"}}
			if last.Content == "tool" {
				deltas = []*schema.Message{{Role: schema.Assistant}, {Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: intPointer(0), ID: "stream-time", Type: "function", Function: schema.FunctionCall{Name: "get_current_time", Arguments: "{"}}}}, {ToolCalls: []schema.ToolCall{{Index: intPointer(0), Function: schema.FunctionCall{Arguments: "}"}}}}}
			}
			if last.Role == schema.Tool {
				deltas = []*schema.Message{answer}
			}
			for _, delta := range deltas {
				payload, _ := json.Marshal(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
				fmt.Fprintf(w, "data: %s\n\n", payload)
				w.(http.Flusher).Flush()
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "message": answer, "finish_reason": "stop"}}})
	}))
	t.Cleanup(api.Close)
	return []string{"ONCALL_CHAT_MODEL_BASE_URL=" + api.URL + "/v1", "ONCALL_CHAT_MODEL_MODEL=fixture", "ONCALL_CHAT_MODEL_API_KEY=fixture-only", "ONCALL_CHAT_MODEL_TIMEOUT=2s", "ONCALL_EMBEDDING_BASE_URL=" + api.URL + "/api/v3", "ONCALL_EMBEDDING_MODEL=doubao-embedding-vision-251215", "ONCALL_EMBEDDING_API_KEY=fixture-only", "ONCALL_MILVUS_ADDRESS=" + listener.Addr().String(), "ONCALL_MILVUS_COLLECTION=fixture", "ONCALL_TIME_ZONE=Asia/Shanghai"}
}
func intPointer(v int) *int { return &v }

func command(t *testing.T, env []string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = t.TempDir()
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "ONCALL_") && !strings.HasPrefix(entry, "ARK_API_KEY=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	return cmd
}
func invoke(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	cmd := command(t, env, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	return out.String(), stderr.String(), err
}

func TestCLIInvokeStreamAndToolWithRealAdapters(t *testing.T) {
	env := fixtures(t)
	for _, args := range [][]string{{"--query", "recover"}, {"--input", `{"id":"demo","query":"recover","history":[{"role":"user","content":"earlier"}]}`}, {"--query", "tool"}, {"--query", "recover", "--stream"}, {"--query", "tool", "--stream"}} {
		out, stderr, err := invoke(t, env, args...)
		if err != nil || stderr != "" {
			t.Fatalf("CLI: %v %s", err, stderr)
		}
		decoder := json.NewDecoder(strings.NewReader(out))
		count := 0
		answer := ""
		for {
			var msg schema.Message
			err := decoder.Decode(&msg)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(msg.ToolCalls) != 0 {
				t.Fatal("internal tool calls leaked")
			}
			count++
			answer += msg.Content
		}
		if count == 0 || (!strings.Contains(answer, "auth.md") && !strings.Contains(answer, "Asia/Shanghai")) {
			t.Fatalf("unexpected output: %s", out)
		}
	}
	out, stderr, err := invoke(t, env, "--query", "failure")
	if err == nil || out != "" || !strings.Contains(stderr, "chat agent execution failed") || strings.Contains(stderr, "fixture-only") {
		t.Fatalf("unsafe failure: %v %s %s", err, out, stderr)
	}
}

func TestCLIParameterErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"--query", " "}, {"--input", "null"}, {"--input", "{"}, {"--input", `{"query":"q"}`, "--query", "q"}, {"--query", "q", "--history", `[{"role":"system","content":"override"}]`}, {"--history", "[]"}, {"--dev-port", "0"}, {"extra"}} {
		out, stderr, err := invoke(t, nil, args...)
		if err == nil || out != "" || stderr == "" {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	out, stderr, err := invoke(t, nil, "--help")
	if err != nil || out != "" || !strings.Contains(stderr, "Usage") {
		t.Fatal("help failed")
	}
}

func TestCLIDevOutputAndSignal(t *testing.T) {
	env := fixtures(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cmd := command(t, env, "--dev", "--dev-port", fmt.Sprint(port), "--query", "recover")
	out, err := os.Create(filepath.Join(t.TempDir(), "stdout.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	log, err := os.Create(filepath.Join(t.TempDir(), "stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = out, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var ready atomic.Bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(log.Name())
		if strings.Contains(string(data), "waiting for SIGINT/SIGTERM") {
			ready.Store(true)
			break
		}
		select {
		case err := <-done:
			t.Fatalf("early exit: %v %s", err, data)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !ready.Load() {
		t.Fatal("dev CLI did not register and stay alive")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI ignored SIGTERM")
	}
	data, _ := os.ReadFile(out.Name())
	var answer schema.Message
	if json.Unmarshal(data, &answer) != nil || !strings.Contains(answer.Content, "auth.md") {
		t.Fatalf("DevOps polluted stdout: %s", data)
	}
}

func TestCLITotalTimeout(t *testing.T) {
	env := append(fixtures(t), "ONCALL_CHAT_AGENT_TIMEOUT=50ms")
	start := time.Now()
	out, stderr, err := invoke(t, env, "--query", "wait")
	if err == nil || out != "" || !strings.Contains(stderr, "context deadline exceeded") || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout: %v %s %s duration=%v", err, out, stderr, time.Since(start))
	}
}
