package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

const alertsJSON = `{"status":"success","data":{"alerts":[{"labels":{"alertname":"HighCPU","instance":"one"},"annotations":{"summary":"cpu"},"state":"firing","activeAt":"2026-10-19T12:00:00+08:00","value":"1"},{"labels":{"alertname":"HighCPU","instance":"two"},"annotations":{},"state":"pending","activeAt":"2026-10-19T12:01:00+08:00","value":"2"}]}}`

func TestPrometheusRealHTTPAndAuthentication(t *testing.T) {
	for _, auth := range []config.HTTPAuth{{Type: "none"}, {Type: "basic", Username: "reader", Password: "fixture-only"}, {Type: "bearer", BearerToken: "fixture-only"}} {
		t.Run(auth.Type, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/prefix/api/v1/alerts" {
					t.Error("wrong alerts endpoint")
				}
				if auth.Type == "basic" {
					user, password, ok := r.BasicAuth()
					if !ok || user != "reader" || password != "fixture-only" {
						t.Error("missing basic auth")
					}
				}
				if auth.Type == "bearer" && r.Header.Get("Authorization") != "Bearer fixture-only" {
					t.Error("missing bearer auth")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, alertsJSON)
			}))
			defer server.Close()
			p, err := client.NewPrometheus(config.ToolHTTP{URL: server.URL + "/prefix/", Auth: auth, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			for i := 0; i < 2; i++ {
				r, err := p.Alerts(context.Background())
				if err != nil || len(r.Alerts) != 2 || r.Alerts[0].Labels["instance"] != "one" || r.Alerts[1].Labels["instance"] != "two" || r.Source != server.URL+"/prefix/api/v1/alerts" {
					t.Fatalf("alerts %+v %v", r, err)
				}
			}
			if calls.Load() != 2 {
				t.Fatal("calls missing")
			}
		})
	}
}

func TestPrometheusFailuresAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"empty", `{"status":"success","data":{"alerts":[]}}`, 200, nil},
		{"unauthorized", "fixture-private-secret", 401, operations.ErrAuthentication}, {"forbidden", "private", 403, operations.ErrAuthentication},
		{"server", "private", 500, operations.ErrUpstream}, {"api_error", `{"status":"error","error":"private"}`, 200, operations.ErrUpstream},
		{"json", "bad", 200, operations.ErrInvalidResponse}, {"missing", `{"status":"success","data":{}}`, 200, operations.ErrInvalidResponse},
		{"null", `{"status":"success","data":{"alerts":null}}`, 200, operations.ErrInvalidResponse},
		{"bad_alert", `{"status":"success","data":{"alerts":[{}]}}`, 200, operations.ErrInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			p, _ := client.NewPrometheus(config.ToolHTTP{URL: server.URL, Auth: config.HTTPAuth{Type: "none"}, Timeout: time.Second})
			defer p.Close()
			r, err := p.Alerts(context.Background())
			if tc.want == nil {
				if err != nil || r.Alerts == nil || len(r.Alerts) != 0 {
					t.Fatalf("empty result %+v %v", r, err)
				}
			} else if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	p, _ := client.NewPrometheus(config.ToolHTTP{URL: server.URL, Auth: config.HTTPAuth{Type: "none"}, Timeout: 20 * time.Millisecond})
	defer p.Close()
	if _, err := p.Alerts(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Alerts(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

const searchSchema = `{"type":"object","properties":{"Region":{"type":"string"},"TopicId":{"type":"string"},"Query":{"type":"string"},"From":{"type":"number"},"To":{"type":"number"},"Limit":{"type":"integer"}},"required":["Region","Query","From","To"]}`

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"params"`
}

func rpcReply(w http.ResponseWriter, id json.RawMessage, result any, sse bool) {
	response := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(response)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

type mcpOptions struct {
	auth    config.HTTPAuth
	schema  string
	noTool  bool
	session bool
	sse     bool
	call    func(http.ResponseWriter, *http.Request, rpcRequest)
}

type mcpCounts struct{ initialize, listed, calls, closed atomic.Int32 }

func mcpFixture(t *testing.T, opts mcpOptions) (config.LogMCP, *mcpCounts) {
	t.Helper()
	counts := &mcpCounts{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Error("wrong MCP path")
		}
		switch opts.auth.Type {
		case "basic":
			user, password, ok := r.BasicAuth()
			if !ok || user != opts.auth.Username || password != opts.auth.Password {
				t.Error("basic auth missing")
			}
		case "bearer":
			if r.Header.Get("Authorization") != "Bearer "+opts.auth.BearerToken {
				t.Error("bearer auth missing")
			}
		case "header":
			if r.Header.Get(opts.auth.HeaderName) != opts.auth.HeaderValue {
				t.Error("custom auth missing")
			}
		}
		if r.Method == "DELETE" {
			counts.closed.Add(1)
			w.WriteHeader(200)
			return
		}
		var request rpcRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid JSON-RPC")
			return
		}
		switch request.Method {
		case "initialize":
			counts.initialize.Add(1)
			if opts.session {
				w.Header().Set("Mcp-Session-Id", "fixture-session")
			}
			rpcReply(w, request.ID, map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fixture", "version": "1"}}, false)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			counts.listed.Add(1)
			schema := opts.schema
			if schema == "" {
				schema = searchSchema
			}
			tools := []any{}
			if !opts.noTool {
				tools = append(tools, map[string]any{"name": "SearchLog", "inputSchema": json.RawMessage(schema)})
			}
			rpcReply(w, request.ID, map[string]any{"tools": tools}, false)
		case "tools/call":
			counts.calls.Add(1)
			if request.Params.Name != "SearchLog" {
				t.Error("wrong remote tool")
			}
			if opts.call != nil {
				opts.call(w, r, request)
				return
			}
			rpcReply(w, request.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": `[{"Time":1792382400000,"Source":"host-a","LogJson":"{\"level\":\"ERROR\"}"}]`}}}, opts.sse)
		default:
			w.WriteHeader(501)
		}
	}))
	t.Cleanup(s.Close)
	cfg := logConfig()
	cfg.HTTP.URL = s.URL + "/mcp"
	cfg.HTTP.Auth = opts.auth
	if cfg.HTTP.Auth.Type == "" {
		cfg.HTTP.Auth.Type = "none"
	}
	return cfg, counts
}

func TestMCPHandshakeAuthSchemaReuseAndConcurrentCalls(t *testing.T) {
	for _, auth := range []config.HTTPAuth{{Type: "none"}, {Type: "basic", Username: "reader", Password: "fixture-only"}, {Type: "bearer", BearerToken: "fixture-only"}, {Type: "header", HeaderName: "X-API-Key", HeaderValue: "fixture-only"}} {
		t.Run(auth.Type, func(t *testing.T) {
			cfg, counts := mcpFixture(t, mcpOptions{auth: auth, session: true, sse: true})
			c, err := client.NewLogMCP(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defs := c.Tools()
			defs[0].InputSchema[0] = 'x'
			if !json.Valid(c.Tools()[0].InputSchema) {
				t.Fatal("mutable metadata escaped")
			}
			service, _ := operations.NewLogService(c, cfg, nil)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, err := service.Query(context.Background(), &operations.LogInput{Query: "*"})
					if err != nil || len(result.Records) != 1 || !strings.Contains(string(result.Records[0]), "host-a") {
						t.Errorf("query %+v %v", result, err)
					}
				}()
			}
			wg.Wait()
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if counts.initialize.Load() != 1 || counts.listed.Load() != 1 || counts.calls.Load() != 8 || counts.closed.Load() != 1 {
				t.Fatal("client was not reused or closed once")
			}
		})
	}
}

func TestMCPRejectsMissingOrIncompatibleSearchLog(t *testing.T) {
	for _, opts := range []mcpOptions{{noTool: true, session: true}, {schema: `{"type":"object","properties":{}}`, session: true}, {schema: strings.Replace(searchSchema, `"type":"integer"`, `"type":"string"`, 1), session: true}} {
		cfg, counts := mcpFixture(t, opts)
		if _, err := client.NewLogMCP(context.Background(), cfg); !errors.Is(err, operations.ErrInvalidResponse) {
			t.Fatalf("bad schema: %v", err)
		}
		if counts.closed.Load() != 1 {
			t.Fatal("initialization failure leaked session")
		}
	}
}

func TestMCPResultShapesFailuresAndPerRequestIsolation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result any
		want   error
	}{
		{"empty", map[string]any{"content": []any{map[string]any{"type": "text", "text": "[]"}}}, nil},
		{"structured", map[string]any{"structuredContent": map[string]any{"records": []any{map[string]any{"count": 3}}}, "content": []any{map[string]any{"type": "text", "text": "invalid fallback"}}}, nil},
		{"truncated", map[string]any{"content": []any{map[string]any{"type": "text", "text": "[{...(truncated)"}}}, operations.ErrInvalidResponse},
		{"wrong_shape", map[string]any{"content": []any{map[string]any{"type": "text", "text": "{}"}}}, operations.ErrInvalidResponse},
		{"null", map[string]any{"content": []any{map[string]any{"type": "text", "text": "null"}}}, operations.ErrInvalidResponse},
		{"scalar_record", map[string]any{"content": []any{map[string]any{"type": "text", "text": "[null]"}}}, operations.ErrInvalidResponse},
		{"auth", map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": `{"code":"AuthFailure.SecretIdNotFound","message":"fixture-private-secret"}`}}}, operations.ErrAuthentication},
		{"error", map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "fixture-private-secret"}}}, operations.ErrUpstream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := mcpFixture(t, mcpOptions{call: func(w http.ResponseWriter, r *http.Request, q rpcRequest) { rpcReply(w, q.ID, tc.result, false) }})
			c, err := client.NewLogMCP(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			records, err := c.Search(context.Background(), operations.LogRequest{Query: "*"})
			if tc.want == nil {
				if err != nil || records == nil {
					t.Fatalf("result %v %v", records, err)
				}
			} else if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private") {
				t.Fatalf("result failure %v", err)
			}
		})
	}
	cfg, _ := mcpFixture(t, mcpOptions{call: func(w http.ResponseWriter, r *http.Request, q rpcRequest) {
		if q.Params.Arguments["Query"] == "denied" {
			w.WriteHeader(401)
			io.WriteString(w, "fixture-private-secret")
			return
		}
		rpcReply(w, q.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "[]"}}}, false)
	}})
	c, err := client.NewLogMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := "*"
			if i%2 == 0 {
				q = "denied"
			}
			_, err := c.Search(context.Background(), operations.LogRequest{Query: q})
			if q == "denied" && !errors.Is(err, operations.ErrAuthentication) || q == "*" && err != nil {
				t.Errorf("cross-request error leakage: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestMCPCancelTimeoutAndCloseReleaseRequests(t *testing.T) {
	entered := make(chan struct{}, 2)
	cfg, _ := mcpFixture(t, mcpOptions{call: func(w http.ResponseWriter, r *http.Request, q rpcRequest) {
		entered <- struct{}{}
		<-r.Context().Done()
	}})
	cfg.HTTP.Timeout = 50 * time.Millisecond
	c, err := client.NewLogMCP(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Search(context.Background(), operations.LogRequest{Query: "*"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Search(ctx, operations.LogRequest{Query: "*"}); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	go func() { _, err := c.Search(context.Background(), operations.LogRequest{Query: "*"}); done <- err }()
	<-entered
	_ = c.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close leaked request")
	}
}
