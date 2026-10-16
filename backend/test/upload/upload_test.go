package upload_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
)

func startAPI(t *testing.T, service *knowledge.KnowledgeService, max int64) string {
	t.Helper()
	server := ghttp.GetServer("upload-test-" + guid.S())
	server.SetDumpRouterMap(false)
	server.SetAccessLogEnabled(false)
	server.SetErrorLogEnabled(false)
	server.SetClientMaxBodySize(max + middleware.MultipartOverhead)
	server.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(health.NewV1())
		group.Group("", func(files *ghttp.RouterGroup) {
			files.Middleware(middleware.UploadBody(max))
			files.Bind(upload.NewV1(service))
		})
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err = server.SetListener(listener); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return "http://" + listener.Addr().String()
}

func localAPI(t *testing.T, store *testutil.MemoryStore, max int64) string {
	t.Helper()
	service, err := testutil.NewKnowledgeService(config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: max, ChunkSize: 20, ChunkOverlap: 4, BatchSize: 2}, store)
	if err != nil {
		t.Fatal(err)
	}
	return startAPI(t, service, max)
}

type envelope struct {
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func multipartRequest(t *testing.T, api string, names, fields, contents []string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i, name := range names {
		part, err := writer.CreateFormFile(fields[i], name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(part, contents[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, api+"/api/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
func send(t *testing.T, req *http.Request, status int) envelope {
	t.Helper()
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("status=%d want=%d body=%s", response.StatusCode, status, data)
	}
	var result envelope
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid envelope %q: %v", data, err)
	}
	if strings.Contains(string(data), "fixture-secret") {
		t.Fatal("dependency error leaked")
	}
	return result
}
func uploadFile(t *testing.T, api, name, text string, status int) envelope {
	t.Helper()
	return send(t, multipartRequest(t, api, []string{name}, []string{"file"}, []string{text}), status)
}

func TestUploadContractAndUpdate(t *testing.T) {
	store := &testutil.MemoryStore{}
	api := localAPI(t, store, 4096)
	var first knowledge.Result
	text := "# 标题\n文档内容 😀。\n" + strings.Repeat("hello ", 10)
	for i := 0; i < 2; i++ {
		response := uploadFile(t, api, "../../guide.MD", text, 200)
		var result knowledge.Result
		if err := json.Unmarshal(response.Data, &result); err != nil {
			t.Fatal(err)
		}
		if response.Message != "OK" || result.Source != "guide.MD" || result.ChunkCount < 2 || !filepath.IsAbs(result.FilePath) || result.FileName != filepath.Base(result.FilePath) || result.FileSize != int64(len(text)) || len(result.ContentHash) != 64 {
			t.Fatalf("unexpected response %+v", result)
		}
		info, err := os.Stat(result.FilePath)
		if err != nil || info.Size() != result.FileSize {
			t.Fatalf("saved stat %v", err)
		}
		saved, err := os.ReadFile(result.FilePath)
		if err != nil || string(saved) != text {
			t.Fatal("saved content differs")
		}
		if i == 0 {
			first = result
		} else if result != first {
			t.Fatal("duplicate changed response")
		}
		if len(store.Snapshot()) != result.ChunkCount {
			t.Fatal("duplicate chunks")
		}
	}
	uploadFile(t, api, "guide.MD", "updated", 200)
	docs := store.Snapshot()
	if len(docs) != 1 || docs[0].Content != "updated" {
		t.Fatal("old version not cleaned")
	}
	if _, err := os.Stat(first.FilePath); err != nil {
		t.Fatal("old saved file should be retained")
	}
	req, err := http.NewRequest(http.MethodGet, api+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	send(t, req, 200)
}

func TestUploadValidation(t *testing.T) {
	api := localAPI(t, &testutil.MemoryStore{}, 64)
	for _, tc := range []struct {
		name, content string
		status        int
	}{
		{"a.txt", "", 400}, {"a.md", " \t\n", 400}, {"a.txt", string([]byte{0xff}), 400}, {"a.txt", "x\x00y", 400},
		{"a.pdf", "hello", 415}, {"a.txt", strings.Repeat("x", 65), 413}, {"a.markdown", "hello", 200},
	} {
		t.Run(tc.name+"_"+tc.content[:min(len(tc.content), 3)], func(t *testing.T) { uploadFile(t, api, tc.name, tc.content, tc.status) })
	}
	for _, tc := range []struct{ names, fields, contents []string }{
		{}, {[]string{"a.md"}, []string{"wrong"}, []string{"x"}},
		{[]string{"a.md", "b.txt"}, []string{"file", "file"}, []string{"x", "y"}},
		{[]string{"a.md", "b.txt"}, []string{"file", "other"}, []string{"x", "y"}},
	} {
		send(t, multipartRequest(t, api, tc.names, tc.fields, tc.contents), 400)
	}
	for _, tc := range []struct {
		kind, body string
		status     int
	}{
		{"multipart/form-data; boundary=missing", "broken", 400}, {"application/json", "{}", 415}, {"", "", 400},
	} {
		req, err := http.NewRequest(http.MethodPost, api+"/api/upload", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", tc.kind)
		send(t, req, tc.status)
	}
	// Unknown-length bodies must still be bounded while multipart is being parsed.
	req := multipartRequest(t, api, []string{"a.txt"}, []string{"file"}, []string{strings.Repeat("x", int(middleware.MultipartOverhead)+100)})
	req.ContentLength = -1
	send(t, req, 413)
}

func TestUploadRetryableErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		store  *testutil.MemoryStore
		status int
	}{
		{"batch", &testutil.MemoryStore{FailBatch: 1}, 503},
		{"cleanup", &testutil.MemoryStore{FailDelete: true}, 503},
		{"timeout", &testutil.MemoryStore{BeforeStore: func(context.Context) error { return context.DeadlineExceeded }}, 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := localAPI(t, tc.store, 128)
			body := uploadFile(t, api, "a.md", "valid document", tc.status)
			var data struct {
				Retryable bool `json:"retryable"`
 Stage string `json:"stage"`
			}
			if err := json.Unmarshal(body.Data, &data); err != nil || !data.Retryable || data.Stage != "ingest" {
				t.Fatalf("not retryable: %s", body.Data)
			}
		})
	}
}

func TestUploadPropagatesCancellation(t *testing.T) {
	entered, finished := make(chan struct{}), make(chan struct{})
	store := &testutil.MemoryStore{BeforeStore: func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}}
	api := localAPI(t, store, 128)
	req := multipartRequest(t, api, []string{"cancel.md"}, []string{"file"}, []string{"cancel this upload"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)
	done := make(chan error, 1)
	go func() {
		res, err := http.DefaultClient.Do(req)
		if res != nil {
			_ = res.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not enter store")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("context cancellation was not propagated")
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled HTTP request should fail")
	}
	if len(store.Snapshot()) != 0 {
		t.Fatal("cancelled upload stored chunks")
	}
}
