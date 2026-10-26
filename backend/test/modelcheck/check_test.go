package modelcheck_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/modelcheck"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func newModel(t *testing.T, handler http.HandlerFunc, timeout time.Duration) *client.OpenAI {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	model, err := client.NewOpenAI(context.Background(), config.ChatModel{
		BaseURL: server.URL + "/v1", Model: "actual-model-id", APIKey: "test-key", Timeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(model.Close)
	return model
}

func TestCheckModelIDAndEinoCompletion(t *testing.T) {
	var catalogCalls, chatCalls atomic.Int32
	model := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing model authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			catalogCalls.Add(1)
			if r.Method != http.MethodGet {
				t.Error("model catalog must use GET")
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"actual-model-id"},{"id":"another-model"}]}`)
		case "/v1/chat/completions":
			chatCalls.Add(1)
			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "actual-model-id" {
				t.Error("completion did not use the configured model ID")
			}
			_, _ = io.WriteString(w, `{"id":"check","model":"actual-model-id","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}, time.Second)
	result, err := modelcheck.Check(context.Background(), model)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfiguredModel != "actual-model-id" || !result.ModelIDVerified || !result.ChatCompletionAvailable || len(result.AvailableModels) != 2 || catalogCalls.Load() != 1 || chatCalls.Load() != 1 {
		t.Fatalf("unexpected model check result: %+v", result)
	}
}

func TestCatalogMustVerifyExactModelID(t *testing.T) {
	for _, tt := range []struct {
		name, catalog string
		status        int
		want          string
	}{
		{"wrong ID", `{"data":[{"id":"different-model"}]}`, 200, "not found"},
		{"empty list", `{"data":[]}`, 200, "not found"},
		{"invalid JSON", `invalid`, 200, "invalid model catalog JSON"},
		{"unauthorized", `private test-key`, 401, "HTTP 401"},
		{"unsupported catalog", `not supported`, 404, "HTTP 404"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var chatCalls atomic.Int32
			model := newModel(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					chatCalls.Add(1)
					t.Error("must not generate before model ID verification")
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.catalog)
			}, time.Second)
			_, err := modelcheck.Check(context.Background(), model)
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "test-key") || chatCalls.Load() != 0 {
				t.Fatalf("expected safe %q error before generation, got %v", tt.want, err)
			}
		})
	}
}

func TestModelCheckCatalogTimeout(t *testing.T) {
	model := newModel(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := modelcheck.Check(ctx, model)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestCatalogAloneDoesNotProveChatWorks(t *testing.T) {
	model := newModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"actual-model-id"}]}`)
			return
		}
		http.Error(w, "provider failure", 500)
	}, time.Second)
	_, err := modelcheck.Check(context.Background(), model)
	if !errors.Is(err, client.ErrModelRequest) {
		t.Fatalf("expected completion failure, got %v", err)
	}
}
