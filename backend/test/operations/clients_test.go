package operations_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
