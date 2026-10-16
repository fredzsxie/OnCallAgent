package chat_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
)

func TestSSEConnectedBeforeWaitingForSession(t *testing.T) {
	var calls atomic.Int32
	m := &scriptModel{generate: func(_ context.Context, in []*schema.Message) (*schema.Message, error) {
		calls.Add(1)
		if len(in) != 4 || in[2].Content != "previous answer" {
			t.Error("queued request did not see committed history")
		}
		return schema.AssistantMessage("next answer", nil), nil
	}}
	service, memory := graphService(t, m, time.Second)
	turn, err := memory.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Close()
	api := startAPI(t, service)
	r := post(t, api+"/api/chat_stream", "a", "next")
	defer r.Body.Close()
	reader := bufio.NewReader(r.Body)
	var first strings.Builder
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		first.WriteString(line)
	}
	if !strings.HasPrefix(first.String(), "event: connected\n") || calls.Load() != 0 {
		t.Fatal("connected waited for the model/session lock")
	}
	turn.Commit("previous question", "previous answer")
	turn.Close()
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "event: done\n") {
		t.Fatalf("queued stream failed: %s %v", rest, err)
	}
	if len(snapshot(t, memory, "a")) != 4 {
		t.Fatal("queued stream did not save a complete turn")
	}
}

func TestSSESlowClientWriteDeadlineReleasesSession(t *testing.T) {
	entered := make(chan struct{})
	m := &scriptModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		close(entered)
		return schema.AssistantMessage(strings.Repeat("x", 16<<20), nil), nil
	}}
	service, memory := graphService(t, m, 20*time.Second)
	api := startAPI(t, service)
	r := post(t, api+"/api/chat_stream", "slow", "q")
	defer r.Body.Close()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	turn, err := memory.Acquire(ctx, "slow")
	if err != nil {
		t.Fatal("blocked write kept the session locked past its deadline")
	}
	defer turn.Close()
	if len(turn.History()) != 0 {
		t.Fatal("slow failed write saved a complete turn")
	}
	if elapsed := time.Since(start); elapsed < 3*time.Second || elapsed > 8*time.Second {
		t.Fatalf("unexpected write deadline duration: %v", elapsed)
	}
}

type scriptModel struct {
	generate func(context.Context, []*schema.Message) (*schema.Message, error)
	stream   func(context.Context, []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

func (m *scriptModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	return &clone, nil
}
func (m *scriptModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.generate(ctx, in)
}
func (m *scriptModel) Stream(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.stream != nil {
		return m.stream(ctx, in)
	}
	msg, err := m.generate(ctx, in)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func graphService(t *testing.T, m model.ToolCallingChatModel, timeout time.Duration) (*chatservice.ChatService, *mem.Store) {
	t.Helper()
	graph, err := pipeline.BuildChatAgent(context.Background(), pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: timeout}, TimeZone: "Asia/Shanghai"}, pipeline.Dependencies{Model: m, Retriever: emptyRetriever{}})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: 30 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := chatservice.NewChatService(graph, memory, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return service, memory
}
func snapshot(t *testing.T, m *mem.Store, id string) []*schema.Message {
	t.Helper()
	turn, err := m.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Close()
	return turn.History()
}

type sseEvent struct {
	name string
	data map[string]any
}

func readSSE(t *testing.T, r *http.Response) []sseEvent {
	t.Helper()
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || r.Header.Get("Cache-Control") != "no-cache" || r.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("invalid SSE headers: %d %v %s", r.StatusCode, r.Header, body)
	}
	if !strings.HasSuffix(string(body), "\n\n") {
		t.Fatalf("missing SSE boundary or appended JSON: %s", body)
	}
	events := []sseEvent{}
	for _, frame := range strings.Split(strings.TrimSuffix(string(body), "\n\n"), "\n\n") {
		lines := strings.Split(frame, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("invalid frame: %q", frame)
		}
		var data map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &data) != nil {
			t.Fatal("data is not a JSON object")
		}
		events = append(events, sseEvent{strings.TrimPrefix(lines[0], "event: "), data})
	}
	return events
}
func post(t *testing.T, url, id, question string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"Id": id, "Question": question})
	response, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestSSEAndInvokeShareGraphMemoryAndPreserveText(t *testing.T) {
	text := "first line\nsecond \"quoted\" line\\path\n"
	m := &scriptModel{generate: func(_ context.Context, in []*schema.Message) (*schema.Message, error) {
		last := in[len(in)-1].Content
		switch last {
		case "remember":
			if len(in) != 2 {
				t.Error("unexpected initial history")
			}
			return schema.AssistantMessage("Aurora", nil), nil
		case "recall":
			if len(in) == 2 {
				return schema.AssistantMessage("unknown", nil), nil
			}
			if len(in) != 4 || in[2].Role != schema.Assistant || in[2].Content != "Aurora" {
				t.Error("ordinary history missing")
			}
			return schema.AssistantMessage(text, nil), nil
		default:
			if len(in) != 6 || in[4].Content != text || in[4].Role != schema.Assistant {
				t.Error("stream history missing")
			}
			return schema.AssistantMessage("okay", nil), nil
		}
	}}
	service, memory := graphService(t, m, time.Second)
	api := startAPI(t, service)
	r := post(t, api+"/api/chat", "a", "remember")
	r.Body.Close()
	events := readSSE(t, post(t, api+"/api/chat_stream", "a", "recall"))
	if len(events) != 3 || events[0].name != "connected" || events[1].name != "message" || events[1].data["content"] != text || events[2].name != "done" {
		t.Fatalf("wrong events: %v", events)
	}
	r = post(t, api+"/api/chat", "a", "next")
	r.Body.Close()
	if len(snapshot(t, memory, "a")) != 6 {
		t.Fatal("wrong saved complete turns")
	}
	events = readSSE(t, post(t, api+"/api/chat_stream", "b", "recall"))
	if events[1].data["content"] != "unknown" {
		t.Fatal("session history leaked")
	}
}

func TestSSEInvalidInputRemainsHTTPJSON(t *testing.T) {
	var calls atomic.Int32
	m := &scriptModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls.Add(1)
		return schema.AssistantMessage("unused", nil), nil
	}}
	service, _ := graphService(t, m, time.Second)
	api := startAPI(t, service)
	for _, payload := range []string{`{}`, `{"Id":"a","Question":" "}`, `{"Id":" ","Question":"q"}`, `{invalid`} {
		r, err := http.Post(api+"/api/chat_stream", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 400 || !json.Valid(body) || strings.Contains(string(body), "event:") {
			t.Fatalf("invalid input: %d %s", r.StatusCode, body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached Agent")
	}
}

func TestSSEAgentInitializationFailureAndTimeout(t *testing.T) {
	for _, mode := range []string{"init", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			finished := make(chan struct{})
			m := &scriptModel{stream: func(ctx context.Context, _ []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
				if mode == "init" {
					close(finished)
					return nil, errors.New("private-secret")
				}
				r, w := schema.Pipe[*schema.Message](0)
				go func() { defer close(finished); defer w.Close(); <-ctx.Done(); w.Send(nil, ctx.Err()) }()
				return r, nil
			}}
			service, memory := graphService(t, m, 40*time.Millisecond)
			api := startAPI(t, service)
			events := readSSE(t, post(t, api+"/api/chat_stream", "a", "q"))
			if len(events) != 2 || events[0].name != "connected" || events[1].name != "error" {
				t.Fatalf("unexpected failure sequence: %v", events)
			}
			message := events[1].data["message"].(string)
			if strings.Contains(message, "private-secret") || (mode == "timeout" && !strings.Contains(message, "timed out")) {
				t.Fatalf("unsafe error: %s", message)
			}
			if len(snapshot(t, memory, "a")) != 0 {
				t.Fatal("failed stream saved history")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("upstream not closed")
			}
		})
	}
}

func TestSSEClientCancellationReleasesUpstreamAndSession(t *testing.T) {
	entered, finished := make(chan struct{}), make(chan struct{})
	m := &scriptModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("recovered", nil), nil
	}, stream: func(ctx context.Context, _ []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		close(entered)
		r, w := schema.Pipe[*schema.Message](0)
		go func() { defer close(finished); defer w.Close(); <-ctx.Done(); w.Send(nil, ctx.Err()) }()
		return r, nil
	}}
	service, memory := graphService(t, m, time.Second)
	api := startAPI(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/chat_stream", strings.NewReader(`{"Id":"a","Question":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("model not entered")
	}
	cancel()
	r.Body.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not close model stream")
	}
	if len(snapshot(t, memory, "a")) != 0 {
		t.Fatal("cancelled turn stored")
	}
	if _, err := service.Chat(context.Background(), "a", "retry"); err != nil {
		t.Fatal("session lock leaked")
	}
}

type writeFailure struct {
	http.ResponseWriter
	stage     string
	flushFail bool
}

func (w *writeFailure) SetWriteDeadline(deadline time.Time) error {
	if w.stage == "init" {
		return http.ErrNotSupported
	}
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}
func (w *writeFailure) Write(data []byte) (int, error) {
	if strings.HasPrefix(string(data), "event: "+w.stage+"\n") {
		return 0, io.ErrClosedPipe
	}
	if (w.stage == "flush" && strings.HasPrefix(string(data), "event: message")) || (w.stage == "done_flush" && strings.HasPrefix(string(data), "event: done")) {
		w.flushFail = true
	}
	return w.ResponseWriter.Write(data)
}
func (w *writeFailure) FlushError() error {
	if w.flushFail {
		w.flushFail = false
		return io.ErrClosedPipe
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func TestSSEWriterFailuresDoNotCommit(t *testing.T) {
	for _, stage := range []string{"init", "message", "flush", "done"} {
		t.Run(stage, func(t *testing.T) {
			m := &scriptModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("answer", nil), nil
			}}
			service, memory := graphService(t, m, time.Second)
			api := startAPIWithWriter(t, service, func(w http.ResponseWriter) http.ResponseWriter { return &writeFailure{ResponseWriter: w, stage: stage} })
			r := post(t, api+"/api/chat_stream", "a", "q")
			if stage == "init" {
				body, _ := io.ReadAll(r.Body)
				r.Body.Close()
				if r.StatusCode != 500 || !json.Valid(body) || strings.Contains(string(body), "event:") {
					t.Fatalf("initialization error: %d %s", r.StatusCode, body)
				}
			} else {
				events := readSSE(t, r)
				for _, e := range events {
					if e.name == "done" {
						t.Fatal("failed write reported success")
					}
				}
			}
			if len(snapshot(t, memory, "a")) != 0 {
				t.Fatal("failed write committed history")
			}
		})
	}
}

type partialAgent struct{}

func (partialAgent) Invoke(context.Context, *pipeline.UserMessage, ...compose.Option) (*schema.Message, error) {
	return schema.AssistantMessage("ok", nil), nil
}
func (partialAgent) Stream(context.Context, *pipeline.UserMessage, ...compose.Option) (*schema.StreamReader[*schema.Message], error) {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		if w.Send(schema.AssistantMessage("partial\n", nil), nil) {
			return
		}
		w.Send(nil, errors.New("private-secret"))
	}()
	return r, nil
}
func TestSSEPartialFailureHasErrorAndNoDone(t *testing.T) {
	memory, _ := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: time.Minute}, nil)
	service, _ := chatservice.NewChatService(partialAgent{}, memory, time.Second)
	api := startAPI(t, service)
	events := readSSE(t, post(t, api+"/api/chat_stream", "a", "q"))
	if len(events) != 3 || events[0].name != "connected" || events[1].name != "message" || events[2].name != "error" {
		t.Fatalf("bad partial failure: %v", events)
	}
	if len(snapshot(t, memory, "a")) != 0 {
		t.Fatal("partial answer saved")
	}
}

func TestSSEFailedDoneFlushDoesNotAppendError(t *testing.T) {
	m := &scriptModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("answer", nil), nil
	}}
	service, memory := graphService(t, m, time.Second)
	api := startAPIWithWriter(t, service, func(w http.ResponseWriter) http.ResponseWriter {
		return &writeFailure{ResponseWriter: w, stage: "done_flush"}
	})
	events := readSSE(t, post(t, api+"/api/chat_stream", "a", "q"))
	if len(events) != 3 || events[2].name != "done" {
		t.Fatalf("event appended after terminal frame: %v", events)
	}
	if len(snapshot(t, memory, "a")) != 0 {
		t.Fatal("failed done flush committed history")
	}
}
