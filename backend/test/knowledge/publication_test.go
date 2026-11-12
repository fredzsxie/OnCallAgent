package knowledge_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

type publicationMemory struct {
	testutil.MemoryStore
	failVerify bool
}

func (s *publicationMemory) Verify(ctx context.Context, id, version string, ids []string) error {
	if s.failVerify {
		return errors.New("not yet visible")
	}
	return ctx.Err()
}
func (s *publicationMemory) Chunks(ctx context.Context, id, version string) ([]*schema.Document, error) {
	out := []*schema.Document{}
	for _, d := range s.Snapshot() {
		if d.MetaData["document_id"] == id && d.MetaData["version"] == version {
			out = append(out, d)
		}
	}
	return out, ctx.Err()
}
func TestPublicationProtectsOldVersionAndRecoversCleanup(t *testing.T) {
	ctx := context.Background()
	cfg := ingestionConfig(t)
	cfg.ChunkSize = 200
	c, err := knowledge.OpenCatalog(filepath.Join(cfg.FileDir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	store := &publicationMemory{}
	m, err := knowledge.NewManager(ctx, cfg, c, store, "test:model:3")
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.Ingest(ctx, "sop.md", strings.NewReader("# SOP\n\n检查回源链路。"), knowledge.MetadataPatch{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "ready" || old.ActiveVersion == "" {
		t.Fatal("initial version not published")
	}
	store.failVerify = true
	_, err = m.Ingest(ctx, "sop.md", strings.NewReader("# SOP\n\n新的排查步骤。"), knowledge.MetadataPatch{}, &old.ActiveVersion, false)
	if err == nil {
		t.Fatal("verification failure ignored")
	}
	d, _ := c.Get(ctx, old.ID)
	if d.ActiveVersion != old.ActiveVersion || d.Status != "failed" {
		t.Fatal("failed version replaced old version")
	}
	store.failVerify = false
	store.FailDelete = true
	d, err = m.Ingest(ctx, "sop.md", strings.NewReader("# SOP\n\n新的排查步骤。"), knowledge.MetadataPatch{}, &old.ActiveVersion, false)
	if err != nil || d.ActiveVersion == old.ActiveVersion || d.Status != "cleanup_pending" {
		t.Fatalf("cleanup rolled back publication: %+v %v", d, err)
	}
	detail, err := m.Detail(ctx, d.ID)
	if err != nil || !strings.Contains(detail.Content, "新的") {
		t.Fatal("detail did not follow active version")
	}
	store.FailDelete = false
	if err = m.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = c.Get(ctx, d.ID)
	if d.Status != "ready" {
		t.Fatal("cleanup not retried")
	}
	for _, chunk := range store.Snapshot() {
		if chunk.MetaData["version"] != d.ActiveVersion {
			t.Fatal("old chunks survived cleanup")
		}
	}
}
func TestV2SchemaChecksFunctionAndAnalyzer(t *testing.T) {
	s := knowledge.SchemaV2("v2", 3)
	if diff := knowledge.SchemaDiffV2(s, 3); len(diff) != 0 {
		t.Fatal(diff)
	}
	s.Functions[0].InputFieldNames = []string{"content"}
	if len(knowledge.SchemaDiffV2(s, 3)) == 0 {
		t.Fatal("BM25 input mismatch accepted")
	}
	s = knowledge.SchemaV2("v2", 3)
	for _, f := range s.Fields {
		if f.Name == "search_text" {
			f.TypeParams["enable_analyzer"] = "false"
		}
	}
	if len(knowledge.SchemaDiffV2(s, 3)) == 0 {
		t.Fatal("disabled analyzer accepted")
	}
}

func TestFailedFirstUploadCanRetryWithoutSourceConflict(t *testing.T) {
	ctx := context.Background()
	cfg := ingestionConfig(t)
	cfg.ChunkSize = 2000
	c, err := knowledge.OpenCatalog(filepath.Join(cfg.FileDir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	store := &publicationMemory{failVerify: true}
	m, err := knowledge.NewManager(ctx, cfg, c, store, "test:3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Ingest(ctx, "first.md", strings.NewReader("first upload"), knowledge.MetadataPatch{}, nil, true); err == nil {
		t.Fatal("expected failed upload")
	}
	store.failVerify = false
	d, err := m.Ingest(ctx, "first.md", strings.NewReader("first upload"), knowledge.MetadataPatch{}, nil, true)
	if err != nil || d.Status != "ready" {
		t.Fatalf("retry blocked: %+v %v", d, err)
	}
}

func TestRetryActiveContentClearsFailedLatestAttempt(t *testing.T) {
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
	d, err := m.Ingest(ctx, "retry.md", strings.NewReader("active content"), knowledge.MetadataPatch{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	store.failVerify = true
	if _, err = m.Ingest(ctx, "retry.md", strings.NewReader("failed content"), knowledge.MetadataPatch{}, &d.ActiveVersion, false); err == nil {
		t.Fatal("expected failed attempt")
	}
	store.failVerify = false
	calls := store.Calls
	retried, err := m.Ingest(ctx, "retry.md", strings.NewReader("active content"), knowledge.MetadataPatch{}, nil, false)
	if err != nil || retried.Status != "ready" || retried.LatestVersion != d.ActiveVersion || store.Calls != calls {
		t.Fatalf("active retry state incorrect: %+v %v", retried, err)
	}
}
