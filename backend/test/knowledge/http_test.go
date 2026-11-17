package knowledge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/router"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

func TestKnowledgeHTTPBrowseSearchUploadAndConflict(t *testing.T) {
	ctx := context.Background()
	cfg := ingestionConfig(t)
	cfg.ChunkSize = 2000
	c, err := knowledge.OpenCatalog(filepath.Join(cfg.FileDir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	store := &publicationMemory{}
	m, err := knowledge.NewManager(ctx, cfg, c, store, "test:3")
	if err != nil {
		t.Fatal(err)
	}
	f := &searchFixture{}
	s, err := knowledge.NewSearchService(c, f, knowledge.SearchConfig{Mode: "hybrid", TopK: 3, DenseMinScore: 0.5, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := knowledge.NewManagedKnowledgeService(cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router.New(ctx, router.Dependencies{Manager: m, Search: s, Upload: legacy, MaxUploadBytes: cfg.MaxUploadBytes}))
	defer server.Close()
	origin := server.URL
	send := func(method, path, contentType string, body io.Reader, want int) json.RawMessage {
		t.Helper()
		req, err := http.NewRequest(method, origin+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.StatusCode, want, raw)
		}
		var envelope struct {
			Message string
			Data    json.RawMessage
		}
		if err = json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	write := func(method, path, version string, want int) json.RawMessage {
		t.Helper()
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		part, _ := writer.CreateFormFile("file", "cdn.md")
		io.WriteString(part, "# 回源\n\n从源站获取内容。\n")
		writer.WriteField("metadata", `{"title":"回源","docType":"glossary","tags":["cdn"],"aliases":["origin"]}`)
		if version != "" {
			writer.WriteField("expectedVersion", version)
		}
		writer.Close()
		return send(method, path, writer.FormDataContentType(), &b, want)
	}
	var d knowledge.DocumentInfo
	if err = json.Unmarshal(write("POST", "/api/knowledge/documents", "", 200), &d); err != nil {
		t.Fatal(err)
	}
	if d.ID == "" || d.ActiveVersion == "" || d.DocType != "glossary" {
		t.Fatalf("invalid document: %+v", d)
	}
	write("POST", "/api/knowledge/documents", "", 409)
	write("PUT", "/api/knowledge/documents/"+d.ID, "wrong", 409)
	write("PUT", "/api/knowledge/documents/"+d.ID, d.ActiveVersion, 200)
	var page knowledge.DocumentPage
	json.Unmarshal(send("GET", "/api/knowledge/documents?docType=glossary&pageSize=1", "", nil, 200), &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("invalid list: %+v", page)
	}
	if err = json.Unmarshal(send("GET", "/api/knowledge/documents", "", nil, 200), &page); err != nil || page.Page != 1 || page.PageSize != 20 {
		t.Fatalf("invalid default pagination: %+v, %v", page, err)
	}
	for _, filter := range []struct {
		query string
		total int
	}{{"tags[]=cdn", 1}, {"tags[]=missing", 0}, {"tags=missing", 0}} {
		if err = json.Unmarshal(send("GET", "/api/knowledge/documents?"+filter.query, "", nil, 200), &page); err != nil || page.Total != filter.total {
			t.Fatalf("filter %s: %+v, %v", filter.query, page, err)
		}
	}
	for _, path := range []string{
		"/api/knowledge/documents?page=0", "/api/knowledge/documents?pageSize=101", "/api/knowledge/documents?page=invalid",
		"/api/knowledge/documents/invalid", "/api/knowledge/documents/" + strings.Repeat("A", 64),
		"/api/knowledge/documents/" + d.ID + "/chunks?page=0",
	} {
		send("GET", path, "", nil, 400)
	}
	var detail knowledge.DocumentInfo
	json.Unmarshal(send("GET", "/api/knowledge/documents/"+d.ID, "", nil, 200), &detail)
	if !strings.Contains(detail.Content, "源站") {
		t.Fatalf("missing original content: %+v", detail)
	}
	send("GET", "/api/knowledge/documents/"+d.ID+"/chunks", "", nil, 200)
	f.docs = store.Snapshot()
	for _, doc := range f.docs {
		doc.WithScore(0.01)
	}
	var result knowledge.SearchResult
	json.Unmarshal(send("POST", "/api/knowledge/search", "application/json", strings.NewReader(`{"query":"origin","mode":"hybrid","topK":5}`), 200), &result)
	if result.Mode != "hybrid" || len(result.Documents) != 1 || result.ScoreType != "rrf" {
		t.Fatalf("invalid search result: %+v", result)
	}
	send("POST", "/api/knowledge/search", "application/json", strings.NewReader(`{"query":"origin","topK":101}`), 400)
	send("POST", "/api/knowledge/search", "application/json", strings.NewReader(`{"query":"origin","mode":"invalid"}`), 400)
	send("GET", "/api/knowledge/documents/"+strings.Repeat("a", 64), "", nil, 404)
	write("POST", "/api/upload", "", 200)
}
