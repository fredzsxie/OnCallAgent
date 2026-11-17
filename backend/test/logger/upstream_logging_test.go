package logger_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func TestUpstreamPrometheusLogsActualRequestAndReadBytesAtInfo(t *testing.T) {
	const body = `{"status":"success","data":{"alerts":[]}}`
	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.RequestURI() + " " + r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	var logs capture
	cfg := settings()
	cfg.Level = "info"
	ctx, _ := testLogger(t, cfg, logger.Options{Console: &logs})
	ctx, id := logger.WithRequest(ctx)
	prom, err := client.NewPrometheus(config.ToolHTTP{
		URL: upstream.URL + "/prefix/", Timeout: time.Second,
		Auth: config.HTTPAuth{Type: "bearer", BearerToken: "private-upstream-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prom.Close()
	result, err := prom.Alerts(ctx)
	if err != nil || result.Alerts == nil || len(result.Alerts) != 0 {
		t.Fatal("logging changed the alerts response", result, err)
	}
	if actual := <-seen; actual != "GET /prefix/api/v1/alerts Bearer private-upstream-token" {
		t.Fatal("logging changed the request URL, query or authentication", actual)
	}
	text := requestLines(logs.String(), id)
	target := "target[" + upstream.URL + "/prefix/api/v1/alerts]"
	if strings.Count(text, target) != 3 || strings.Count(text, "method[GET]") != 3 || strings.Count(text, "上游响应读取结束") != 1 {
		t.Fatal("upstream boundaries lost their target", text)
	}
	for _, want := range []string{"[INFO]", "发起上游请求", "role[alerts]", "code[200]", "outcome[eof]", fmt.Sprintf("response_bytes[%d]", len(body))} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing upstream result %q: %s", want, text)
		}
	}
	if strings.Contains(text, "private-upstream-token") || strings.Contains(text, body) || strings.Contains(text, "Authorization") {
		t.Fatal("upstream logging exposed authentication or a response body", text)
	}
}

func TestUpstreamPrometheusLogsStatusWithoutReadingRejectedBodies(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   string
	}{{401, "authentication_failed"}, {429, "rate_limited"}, {500, "upstream_error"}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":"private-rejected-body"}`)
			}))
			defer upstream.Close()
			var logs capture
			ctx, _ := testLogger(t, settings(), logger.Options{Console: &logs})
			prom, err := client.NewPrometheus(config.ToolHTTP{URL: upstream.URL, Timeout: time.Second, Auth: config.HTTPAuth{Type: "none"}})
			if err != nil {
				t.Fatal(err)
			}
			defer prom.Close()
			if _, err := prom.Alerts(ctx); err == nil {
				t.Fatal("expected an upstream failure")
			}
			text := logs.String()
			for _, want := range []string{fmt.Sprintf("code[%d]", tc.status), "error_type[" + tc.kind + "]", "response_bytes[0]", "outcome[closed]"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing rejected-response observation %q: %s", want, text)
				}
			}
			if strings.Count(text, "上游响应读取结束") != 1 || strings.Contains(text, "private-rejected-body") {
				t.Fatal("response was pre-read or logged twice", text)
			}
		})
	}
}

func TestUpstreamPrometheusBodyCancellationLogsOneReadResult(t *testing.T) {
	const prefix = `{"status":`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, prefix)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	var logs capture
	root, _ := testLogger(t, settings(), logger.Options{Console: &logs})
	ctx, cancel := context.WithCancel(root)
	defer cancel()
	prom, err := client.NewPrometheus(config.ToolHTTP{URL: upstream.URL, Timeout: 3 * time.Second, Auth: config.HTTPAuth{Type: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	defer prom.Close()
	done := make(chan error, 1)
	go func() { _, err := prom.Alerts(ctx); done <- err }()
	await(t, func() bool { return strings.Contains(logs.String(), "收到上游响应头") })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("logging changed cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upstream read survived cancellation")
	}
	text := logs.String()
	if strings.Count(text, "上游响应读取失败") != 1 || !strings.Contains(text, "error_type[cancelled]") || !strings.Contains(text, "outcome[cancelled]") || !strings.Contains(text, "response_bytes[") {
		t.Fatal("cancelled body lost its only terminal result or read count", text)
	}
}

func TestUpstreamPrometheusConnectionFailureDoesNotInventStatus(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()
	var logs capture
	ctx, _ := testLogger(t, settings(), logger.Options{Console: &logs})
	prom, err := client.NewPrometheus(config.ToolHTTP{URL: upstream.URL, Timeout: time.Second, Auth: config.HTTPAuth{Type: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	defer prom.Close()
	if _, err := prom.Alerts(ctx); err == nil {
		t.Fatal("expected a connection failure")
	}
	text := logs.String()
	if strings.Contains(text, "code[") || strings.Contains(text, "response_bytes[") || strings.Count(text, "target["+upstream.URL+"/api/v1/alerts]") != 2 || !strings.Contains(text, "上游连接失败") {
		t.Fatal("connection failure lost its target or invented a response", text)
	}
}

func TestUpstreamLoggingLeavesModelStreamsUnbuffered(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected model endpoint")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"private-first-token\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"private-final-token\"}}]}\n\ndata: [DONE]\n\n")
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	var logs capture
	ctx, _ := testLogger(t, settings(), logger.Options{Console: &logs})
	model, err := client.NewOpenAI(ctx, config.ChatModel{BaseURL: upstream.URL + "/v1", Model: "fixture", APIKey: "private-stream-key", Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	stream, err := model.ToolCallingModel().Stream(ctx, []*schema.Message{schema.UserMessage("private-stream-question")})
	if err != nil {
		t.Fatal(err)
	}
	closeStream := sync.OnceFunc(stream.Close)
	defer closeStream()
	first, err := stream.Recv()
	if err != nil || first == nil || first.Content != "private-first-token" {
		t.Fatal("stream no longer delivers its first token", first, err)
	}
	if strings.Contains(logs.String(), "上游响应读取结束") {
		t.Fatal("logging finished the upstream body before its producer", logs.String())
	}
	close(release)
	var rest strings.Builder
	for {
		part, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rest.WriteString(part.Content)
	}
	if rest.String() != "private-final-token" {
		t.Fatal("stream content changed", rest.String())
	}
	closeStream()
	await(t, func() bool { return strings.Contains(logs.String(), "上游响应读取结束") })
	text := logs.String()
	if strings.Count(text, "上游响应读取结束") != 1 {
		t.Fatal("stream has multiple terminal observations", text)
	}
	for _, secret := range []string{"private-first-token", "private-final-token", "private-stream-key", "private-stream-question"} {
		if strings.Contains(text, secret) {
			t.Fatal("upstream logging exposed model content", secret)
		}
	}
}

func TestUpstreamURLPreviewRemovesCredentialsWithoutChangingURL(t *testing.T) {
	const raw = "https://private-user:private-password@example.test/api/v1/query?query=up&token=private-query-token#private-fragment"
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	preview := logger.URLPreview(u)
	if u.String() != raw || !strings.Contains(preview, "example.test/api/v1/query") || !strings.Contains(preview, "query=up") {
		t.Fatal("URL preview changed the request or lost its destination", preview)
	}
	if strings.Contains(preview, "private-") {
		t.Fatal("URL preview exposed credentials", preview)
	}
	for _, tc := range []struct {
		name   string
		url    *url.URL
		marker string
	}{
		{"nil", nil, ""},
		{"long_path", &url.URL{Scheme: "https", Host: "example.test", Path: "/" + strings.Repeat("x", logger.PreviewLimit)}, "<truncated>"},
		{"long_query", &url.URL{Scheme: "https", Host: "example.test", RawQuery: "query=" + strings.Repeat("x", logger.PreviewLimit)}, "<truncated>"},
		{"invalid_escape", &url.URL{Scheme: "https", Host: "example.test", Path: "/api/v1/query", RawQuery: "token=private-query-token&query=%zz"}, "<invalid>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := ""
			if tc.url != nil {
				original = tc.url.String()
			}
			result := logger.URLPreview(tc.url)
			if !strings.Contains(result, tc.marker) || tc.url == nil && result != "" || strings.Contains(result, "private-") || len(result) > logger.PreviewLimit {
				t.Fatal("unsafe URL preview", result)
			}
			if tc.url != nil && tc.url.String() != original {
				t.Fatal("URL preview changed the destination")
			}
		})
	}
}
