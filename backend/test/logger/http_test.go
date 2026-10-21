package logger_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	controller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
)

type memoryRetriever struct{}

func (memoryRetriever) Retrieve(ctx context.Context, _ string, _ ...einoretriever.Option) ([]*schema.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []*schema.Document{(&schema.Document{ID: "chunk-1", Content: "private-document-body", MetaData: map[string]any{"source": "private-source.md"}}).WithScore(.9)}, nil
}

func localModel() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		question := in.Messages[len(in.Messages)-1].Content
		if question == "__error__" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"private-provider-error"}}`)
			return
		}
		if question == "__slow__" || question == "__timeout__" {
			<-r.Context().Done()
			return
		}
		if !in.Stream {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"private-answer-body"},"finish_reason":"stop"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if question == "__broken_stream__" {
			io.WriteString(w, "data: {broken-private-provider-error}\n\n")
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"private-answer-body\\nsecond line\"}}]}\n\ndata: [DONE]\n\n")
	}))
}

func loggedAPI(t *testing.T, root context.Context, base string, wraps ...func(http.ResponseWriter) http.ResponseWriter) (string, *ghttp.Server) {
	t.Helper()
	model, err := client.NewOpenAI(root, config.ChatModel{BaseURL: base + "/v1", Model: "fixture", APIKey: "private-api-key", Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(model.Close)
	recall, err := retriever.NewMilvusRetriever(memoryRetriever{}, config.Retrieval{TopK: 3, ScoreThreshold: .5})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := pipeline.BuildChatAgent(root, pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: 250 * time.Millisecond}, TimeZone: "Asia/Shanghai"}, pipeline.Dependencies{Model: model.ToolCallingModel(), Retriever: recall})
	if err != nil {
		t.Fatal(err)
	}
	memory, _ := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: time.Minute}, nil)
	chat, err := chatservice.NewChatService(graph, memory, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	streams, _ := sse.New(chat)
	server := ghttp.GetServer("logger-test-" + guid.S())
	middleware.ObserveHTTP(server, root)
	server.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(controller.NewV1(chat, streams))
		group.Bind(health.NewV1())
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetListener(listener); err != nil {
		t.Fatal(err)
	}
	if len(wraps) > 0 {
		server.SetHandler(func(w http.ResponseWriter, r *http.Request) { server.ServeHTTP(wraps[0](w), r) })
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return "http://" + listener.Addr().String(), server
}

func post(t *testing.T, url, question string) (int, string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"Id": "private-session-id", "Question": question})
	response, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header.Get("X-Request-ID"), string(b)
}
func await(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("log condition not met")
}
func requestLines(text, id string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "req="+id) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func exerciseHTTP(t *testing.T, base string) {
	var logs capture
	root, _ := testLogger(t, settings(), logger.Options{Console: &logs})
	url, _ := loggedAPI(t, root, base)
	for _, tc := range []struct {
		path, query, outcome string
		status               int
	}{
		{"chat", "private-question-body", "success", 200},
		{"chat", "__error__", "failed", 502},
		{"chat", "", "failed", 400},
		{"chat_stream", "private-question-body", "success", 200},
		{"chat_stream", "__error__", "failed", 200},
		{"chat_stream", "__broken_stream__", "failed", 200},
		{"chat_stream", "__timeout__", "timeout", 200},
	} {
		t.Run(tc.path+tc.query, func(t *testing.T) {
			status, id, body := post(t, url+"/api/"+tc.path, tc.query)
			if status != tc.status || id == "" {
				t.Fatal(status, id, body)
			}
			await(t, func() bool { return strings.Contains(requestLines(logs.String(), id), "outcome="+tc.outcome) })
			lines := requestLines(logs.String(), id)
			if !strings.Contains(lines, "请求开始") || !strings.Contains(lines, "status="+fmt.Sprint(tc.status)) {
				t.Fatal("missing boundary", lines)
			}
			if tc.path == "chat_stream" {
				if tc.outcome == "success" {
					if !strings.Contains(body, "event: done") || !strings.Contains(lines, "流式响应完成") {
						t.Fatal("stream success", body, lines)
					}
				} else if !strings.Contains(body, "event: error") || strings.Contains(body, "event: done") {
					t.Fatal("stream failure", body)
				}
			}
		})
	}
	response, err := http.Get(url + "/api/private-path-secret?api_key=private-query-key")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	id := response.Header.Get("X-Request-ID")
	await(t, func() bool { return strings.Contains(requestLines(logs.String(), id), "status=404") })
	// Each request must carry its own ID through graph execution and the real SDK transport.
	_, id, _ = post(t, url+"/api/chat", "private-question-body")
	await(t, func() bool { return strings.Contains(requestLines(logs.String(), id), "outcome=success") })
	lines := requestLines(logs.String(), id)
	for _, event := range []string{"请求开始", "开始对话", "知识检索完成", "收到上游响应头", "模型生成完成", "history_committed=true", "请求完成"} {
		if !strings.Contains(lines, event) {
			t.Fatal("missing correlated event", event, lines)
		}
	}
	for _, position := range []string{"[utility/middleware/logging.go:", "[internal/service/chat/chat.go:", "[internal/ai/retriever/retriever.go:", "[internal/ai/agent/chat_pipeline/flow.go:", "[utility/client/logging.go:"} {
		if !strings.Contains(lines, position) {
			t.Fatal("missing business caller", position, lines)
		}
	}
	for _, secret := range []string{"private-question-body", "private-answer-body", "private-document-body", "private-source.md", "private-api-key", "private-session-id", "private-provider-error", "private-path-secret", "private-query-key", "Authorization", "http://"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("sensitive content in logs", secret)
		}
	}
}

func TestActualHTTPGraphLogging(t *testing.T) {
	upstream := localModel()
	defer upstream.Close()
	exerciseHTTP(t, upstream.URL)
}

func TestPostmanHTTPGraphLogging(t *testing.T) {
	base := os.Getenv("ONCALL_LOGGING_SIM_URL")
	if base == "" {
		t.Skip("set ONCALL_LOGGING_SIM_URL to an explicitly started local Postman chat simulation")
	}
	exerciseHTTP(t, strings.TrimRight(base, "/"))
}

func TestHTTPConcurrentIDsAndCancellation(t *testing.T) {
	upstream := localModel()
	defer upstream.Close()
	var logs capture
	root, _ := testLogger(t, settings(), logger.Options{Console: &logs})
	url, _ := loggedAPI(t, root, upstream.URL)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, id, _ := post(t, url+"/api/chat", "private-question-body"); ids <- id }()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if id == "" || seen[id] {
			t.Fatal("duplicate request ID", id)
		}
		seen[id] = true
		await(t, func() bool { return strings.Contains(requestLines(logs.String(), id), "outcome=success") })
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, "POST", url+"/api/chat_stream", strings.NewReader(`{"Id":"cancel-session","Question":"__slow__"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	id := response.Header.Get("X-Request-ID")
	cancel()
	response.Body.Close()
	await(t, func() bool { return strings.Contains(requestLines(logs.String(), id), "outcome=cancelled") })
	if !strings.Contains(requestLines(logs.String(), id), "history_committed=false") {
		t.Fatal("cancelled stream committed history")
	}
}

func TestToolsSharedWrapperLogsNamesWithoutContents(t *testing.T) {
	var out capture
	ctx, _ := testLogger(t, settings(), logger.Options{Console: &out})
	ctx, id := logger.WithRequest(ctx)
	recall, _ := retriever.NewMilvusRetriever(memoryRetriever{}, config.Retrieval{TopK: 3, ScoreThreshold: .5})
	registry, err := tools.NewRegistry(config.Tools{GetCurrentTimeEnabled: true, QueryInternalDocsEnabled: true, TimeZone: "Asia/Shanghai"}, tools.Dependencies{Retriever: recall})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range registry.Tools() {
		info, _ := tool.Info(ctx)
		input := "{}"
		if info.Name == tools.QueryInternalDocs {
			input = `{"query":"private-tool-query"}`
		}
		if _, err := registry.Invoke(ctx, info.Name, input); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out.String(), "req="+id) || !strings.Contains(out.String(), "tool=query_internal_docs") || !strings.Contains(out.String(), "tool=get_current_time") || !strings.Contains(out.String(), "count=1") {
		t.Fatal(out.String())
	}
	for _, secret := range []string{"private-tool-query", "private-document-body", "private-source.md"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("tool contents leaked")
		}
	}
}

type failingWriter struct {
	http.ResponseWriter
	init bool
}

func (w *failingWriter) SetWriteDeadline(deadline time.Time) error {
	if w.init {
		return http.ErrNotSupported
	}
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}
func (w *failingWriter) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), "event: message") {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}
func (w *failingWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func TestSSEWriteAndInitializationFailureLogs(t *testing.T) {
	for _, init := range []bool{false, true} {
		t.Run(fmt.Sprint(init), func(t *testing.T) {
			upstream := localModel()
			defer upstream.Close()
			var logs capture
			root, _ := testLogger(t, settings(), logger.Options{Console: &logs})
			url, _ := loggedAPI(t, root, upstream.URL, func(w http.ResponseWriter) http.ResponseWriter { return &failingWriter{w, init} })
			status, id, body := post(t, url+"/api/chat_stream", "private-question-body")
			want := 200
			if init {
				want = 500
			}
			if status != want {
				t.Fatal(status, body)
			}
			await(t, func() bool { return strings.Contains(requestLines(logs.String(), id), "outcome=failed") })
			if strings.Contains(body, "event: done") || !strings.Contains(requestLines(logs.String(), id), "history_committed=false") {
				t.Fatal("write failure reported success", body, logs.String())
			}
		})
	}
}
