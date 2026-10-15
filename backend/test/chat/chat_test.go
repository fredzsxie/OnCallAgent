package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	controller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	logic "github.com/fredzsxie/OnCallAgent/backend/internal/logic/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
)

// These are local protocol tests of the real Eino adapter, not a runtime mock mode.
func newModel(t *testing.T, handler http.HandlerFunc, timeout time.Duration) *client.OpenAI {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	model, err := client.NewOpenAI(context.Background(), config.ChatModel{
		BaseURL: upstream.URL + "/v1", Model: "test-model", APIKey: "test-key", Timeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(model.Close)
	return model
}

func completion(w http.ResponseWriter, answer string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "test-completion", "object": "chat.completion", "model": "test-model",
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop",
		}},
	})
}

func startAPI(t *testing.T, service *logic.ChatService) string {
	t.Helper()
	server := ghttp.GetServer("chat-test-" + guid.S())
	server.SetDumpRouterMap(false)
	server.SetAccessLogEnabled(false)
	server.SetErrorLogEnabled(false)
	server.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(controller.NewV1(service))
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetListener(listener); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown() })
	return "http://" + listener.Addr().String()
}

func TestChatContractAndReusableEinoModel(t *testing.T) {
	var calls atomic.Int32
	model := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("unexpected OpenAI request method, path or authorization")
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", 400)
			return
		}
		if body.Model != "test-model" || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "你好" {
			t.Errorf("unexpected model or messages: %+v", body)
		}
		completion(w, "你好，我是助手。")
	}, time.Second)
	if calls.Load() != 0 {
		t.Fatal("constructing a model should not send a completion request")
	}
	api := startAPI(t, logic.NewChatService(model))
	httpClient := &http.Client{Timeout: 3 * time.Second}
	for i := 0; i < 2; i++ {
		response, err := httpClient.Post(api+"/api/chat", "application/json", strings.NewReader(`{"Id":"session-1","Question":"你好"}`))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Message string `json:"message"`
			Data    struct {
				Answer string `json:"answer"`
			} `json:"data"`
		}
		err = json.NewDecoder(response.Body).Decode(&body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || body.Message != "OK" || body.Data.Answer != "你好，我是助手。" {
			t.Fatalf("unexpected chat response: status=%d body=%+v error=%v", response.StatusCode, body, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("expected two calls through the same model, got %d", calls.Load())
	}
}

func TestChatRejectsInvalidInputBeforeCallingModel(t *testing.T) {
	var calls atomic.Int32
	model := newModel(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); completion(w, "unused") }, time.Second)
	api := startAPI(t, logic.NewChatService(model))
	for _, payload := range []string{`{}`, `{"Id":"s"}`, `{"Question":"hello"}`, `{"Id":"s","Question":"   "}`, `{"Id":" ","Question":"hello"}`, `{invalid`} {
		response, err := http.Post(api+"/api/chat", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 400 || !json.Valid(body) {
			t.Fatalf("expected HTTP 400 JSON for %s, got %d: %s", payload, response.StatusCode, body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input called the model")
	}
}

func TestChatUpstreamFailures(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		handler http.HandlerFunc
	}{
		{"provider error", 502, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "private test-key", 401) }},
		{"empty answer", 502, func(w http.ResponseWriter, _ *http.Request) { completion(w, " ") }},
		{"timeout", 504, func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := newModel(t, tt.handler, 75*time.Millisecond)
			api := startAPI(t, logic.NewChatService(model))
			response, err := http.Post(api+"/api/chat", "application/json", strings.NewReader(`{"Id":"s","Question":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != tt.status || !json.Valid(body) || strings.Contains(string(body), "test-key") {
				t.Fatalf("unexpected upstream failure: %d %s", response.StatusCode, body)
			}
		})
	}
}

func TestRequestCancellationReachesModel(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	model := newModel(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}, 3*time.Second)
	api := startAPI(t, logic.NewChatService(model))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/chat", strings.NewReader(`{"Id":"s","Question":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	done := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(req)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("model request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP request was not cancelled")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach the upstream model")
	}
}
