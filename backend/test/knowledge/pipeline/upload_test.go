package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/components/document"
	knowledgepipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

func TestLogicalSourceOverridePreservesDocumentIdentity(t *testing.T) {
	cfg := settings(t)
	text := "# Runbook\nCheck the failing dependency."
	physical := write(t, cfg, "versioned_0123456789.md", text)
	write(t, cfg, "runbook.md", text)
	store := &testutil.MemoryStore{}
	graph := build(t, cfg, store)
	expected, err := graph.Invoke(context.Background(), document.Source{URI: "runbook.md"})
	if err != nil {
		t.Fatal(err)
	}
	indexFile := knowledgepipeline.NewFileIndexer(graph)
	actual, err := indexFile(context.Background(), document.Source{URI: physical}, "runbook.md")
	if err != nil || !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(sorted(expected), visible(store)) {
		t.Fatalf("source override changed IDs: %v", err)
	}
	for _, doc := range store.Snapshot() {
		if doc.MetaData["source"] != "runbook.md" {
			t.Fatal("physical name leaked into logical source")
		}
	}
	if knowledgepipeline.NewFileIndexer(nil) != nil {
		t.Fatal("nil graph adapter accepted")
	}
}

func TestLogicalSourceOverrideCannotBypassPhysicalChecks(t *testing.T) {
	cfg := settings(t)
	write(t, cfg, "image.pdf", "valid text")
	outside := write(t, knowledgepipeline.Config{Root: t.TempDir()}, "outside.md", "valid text")
	if err := os.Symlink(outside, filepath.Join(cfg.Root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.Root, "directory.md"), 0700); err != nil {
		t.Fatal(err)
	}
	store := &testutil.MemoryStore{}
	indexFile := knowledgepipeline.NewFileIndexer(build(t, cfg, store))
	for _, filename := range []string{"image.pdf", outside, "../outside.md", "link.md", "directory.md", "missing.md"} {
		if _, err := indexFile(context.Background(), document.Source{URI: filename}, "valid.md"); err == nil {
			t.Fatalf("source override bypassed physical check: %s", filename)
		}
	}
	physical := write(t, cfg, "valid.md", "valid bytes")
	for _, logical := range []string{"", "../outside.md", "/absolute.md", "bad.pdf"} {
		_, err := indexFile(context.Background(), document.Source{URI: physical}, logical)
		if !errors.Is(err, knowledge.ErrInvalidDocument) && !errors.Is(err, knowledge.ErrUnsupportedFormat) {
			t.Fatalf("logical source validation: %v", err)
		}
	}
	if store.Calls != 0 {
		t.Fatal("invalid physical/logical source reached store")
	}
}
