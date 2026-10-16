package embedding_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func embedder(t *testing.T, handler http.HandlerFunc, dim int, timeout time.Duration) *client.Embedding {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	e, err := client.NewEmbedding(context.Background(), config.Embedding{BaseURL: s.URL + "/api/v3", Model: "doubao-embedding-vision-251215", APIKey: "fixture-secret", Timeout: timeout, Dimensions: dim})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestArkMultimodalTextAndDimensions(t *testing.T) {
	e := embedder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/embeddings/multimodal" || r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("wrong endpoint or authentication")
		}
		var body struct {
			Model string                        `json:"model"`
			Input []struct{ Type, Text string } `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "doubao-embedding-vision-251215" || len(body.Input) != 1 || body.Input[0].Type != "text" {
			t.Error("invalid Ark text payload")
		}
		vector := []float64{1, 2, 3}
		if body.Input[0].Text == "second" {
			vector = []float64{3, 2, 1}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"embedding": vector}})
	}, 0, time.Second)
	v, err := e.EmbedStrings(context.Background(), []string{"first", "second"})
	if err != nil || len(v) != 2 || len(v[0]) != 3 || v[1][0] != 3 {
		t.Fatalf("unexpected vectors: %v, %v", v, err)
	}
	if _, err = e.EmbedStrings(context.Background(), []string{"first"}, embedding.WithModel("different-model")); err == nil {
		t.Fatal("model override must fail")
	}
}

func TestEmbeddingRejectsBadVectorsAndProviderErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		dim        int
	}{
		{"dimension", `{"data":{"embedding":[1,2]}}`, 200, 3},
		{"empty", `{"data":{"embedding":[]}}`, 200, 0},
		{"zero", `{"data":{"embedding":[0,0]}}`, 200, 2},
		{"malformed", `not JSON`, 200, 0},
		{"auth", `{"error":{"message":"fixture-secret","type":"auth","code":"Unauthorized"}}`, 401, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := embedder(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}, tt.dim, time.Second)
			_, err := e.EmbedStrings(context.Background(), []string{"hello"})
			if err == nil || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("expected safe error: %v", err)
			}
		})
	}
	for _, v := range [][]float64{{math.NaN()}, {math.Inf(1)}, {math.MaxFloat64}, {1e-100}, {0}} {
		if _, err := client.FloatVector(v, 1); err == nil {
			t.Fatal("unsafe COSINE vector accepted")
		}
	}
}

func TestEmbeddingContextAndTimeout(t *testing.T) {
	e := embedder(t, func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }, 0, 30*time.Millisecond)
	_, err := e.EmbedStrings(context.Background(), []string{"hello"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.EmbedStrings(ctx, []string{"hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled, got %v", err)
	}
}
