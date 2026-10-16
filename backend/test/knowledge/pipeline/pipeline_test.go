package pipeline_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/compose"
	knowledgepipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

func settings(t *testing.T) knowledgepipeline.Config {
	t.Helper()
	return knowledgepipeline.Config{Root: t.TempDir(), Knowledge: config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 4096, ChunkSize: 64, ChunkOverlap: 8, BatchSize: 2}}
}

func build(t *testing.T, cfg knowledgepipeline.Config, store knowledge.DocumentStore) compose.Runnable[document.Source, []string] {
	t.Helper()
	g, err := knowledgepipeline.BuildKnowledgeIndexing(context.Background(), cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func write(t *testing.T, cfg knowledgepipeline.Config, name, text string) string {
	t.Helper()
	filename := filepath.Join(cfg.Root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func sorted(ids []string) []string {
	result := append([]string{}, ids...)
	sort.Strings(result)
	return result
}

func visible(store *testutil.MemoryStore) []string {
	ids := []string{}
	for _, doc := range store.Snapshot() {
		ids = append(ids, doc.ID)
	}
	return sorted(ids)
}

func TestGraphStableIDsAndUploadParity(t *testing.T) {
	cfg := settings(t)
	text := "# 数据库告警\r\n" + strings.Repeat("Check connections 中文🙂。 ", 14) + "\n## Recovery\nRestart safely."
	filename := write(t, cfg, "ops/runbook.md", text)
	store := &testutil.MemoryStore{}
	g := build(t, cfg, store)
	first, err := g.Invoke(context.Background(), document.Source{URI: "ops/runbook.md"})
	if err != nil || len(first) < 3 {
		t.Fatalf("invoke: %v, IDs=%v", err, first)
	}
	second, err := g.Invoke(context.Background(), document.Source{URI: filename})
	if err != nil || !reflect.DeepEqual(first, second) || !reflect.DeepEqual(sorted(first), visible(store)) {
		t.Fatalf("repeat differs: %v", err)
	}
	sum := sha256.Sum256([]byte(text))
	expected, err := knowledge.Split("ops/runbook.md", text, hex.EncodeToString(sum[:]), cfg.Knowledge)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, doc := range expected {
		byID[doc.ID] = i
	}
	for _, doc := range store.Snapshot() {
		i, ok := byID[doc.ID]
		if !ok || !reflect.DeepEqual(doc, expected[i]) {
			t.Fatalf("metadata/content differs: %+v", doc)
		}
		for _, key := range []string{"source", "title", "chunk_index", "content_hash", "document_hash", "document_id", "version"} {
			if _, ok := doc.MetaData[key]; !ok {
				t.Fatalf("missing %s", key)
			}
		}
	}
	for _, size := range store.BatchSizes {
		if size > cfg.Knowledge.BatchSize {
			t.Fatal("unbounded batch")
		}
	}
	if entries, _ := os.ReadDir(cfg.Knowledge.FileDir); len(entries) != 0 {
		t.Fatal("graph saved an upload copy")
	}
	service, err := testutil.NewKnowledgeService(cfg.Knowledge, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(context.Background(), "ops/runbook.md", strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sorted(first), visible(store)) {
		t.Fatal("upload and graph IDs differ")
	}
}

func TestGraphUpdateFailureAndRetry(t *testing.T) {
	for _, failure := range []string{"batch", "cleanup", "acknowledgement"} {
		t.Run(failure, func(t *testing.T) {
			cfg := settings(t)
			write(t, cfg, "runbook.md", "# Old\n"+strings.Repeat("Original content. ", 12))
			store := &testutil.MemoryStore{}
			g := build(t, cfg, store)
			old, err := g.Invoke(context.Background(), document.Source{URI: "runbook.md"})
			if err != nil {
				t.Fatal(err)
			}
			write(t, cfg, "runbook.md", "# New\n"+strings.Repeat("Updated content. ", 20))
			switch failure {
			case "batch":
				store.FailBatch = store.Calls + 2
			case "cleanup":
				store.FailDelete = true
			case "acknowledgement":
				store.BadIDs = true
			}
			_, err = g.Invoke(context.Background(), document.Source{URI: "runbook.md"})
			if !errors.Is(err, knowledge.ErrRetryable) {
				t.Fatalf("failure lost: %v", err)
			}
			remaining := visible(store)
			for _, id := range old {
				if n := sort.SearchStrings(remaining, id); n == len(remaining) || remaining[n] != id {
					t.Fatal("failure removed old chunks")
				}
			}
			store.FailBatch, store.FailDelete, store.BadIDs = 0, false, false
			updated, err := g.Invoke(context.Background(), document.Source{URI: "runbook.md"})
			if err != nil || !reflect.DeepEqual(sorted(updated), visible(store)) {
				t.Fatalf("retry did not converge: %v", err)
			}
			if reflect.DeepEqual(sorted(old), sorted(updated)) {
				t.Fatal("update did not change IDs")
			}
			repeated, err := g.Invoke(context.Background(), document.Source{URI: "runbook.md"})
			if err != nil || !reflect.DeepEqual(updated, repeated) {
				t.Fatalf("updated repeat differs: %v", err)
			}
		})
	}
}

func TestGraphRejectsInvalidDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       error
	}{
		{"empty.md", "\ufeff \n", knowledge.ErrInvalidDocument},
		{"invalid.md", "\xff", knowledge.ErrInvalidDocument},
		{"nul.md", "a\x00b", knowledge.ErrInvalidDocument},
		{"image.png", "hello", knowledge.ErrUnsupportedFormat},
		{"large.txt", strings.Repeat("x", 4097), knowledge.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := settings(t)
			write(t, cfg, tc.name, tc.text)
			store := &testutil.MemoryStore{}
			_, err := build(t, cfg, store).Invoke(context.Background(), document.Source{URI: tc.name})
			if !errors.Is(err, tc.want) || store.Calls != 0 {
				t.Fatalf("invalid document reached store: %v", err)
			}
		})
	}
	cfg := settings(t)
	store := &testutil.MemoryStore{}
	g := build(t, cfg, store)
	outside := write(t, knowledgepipeline.Config{Root: t.TempDir()}, "outside.md", "outside")
	if err := os.Symlink(outside, filepath.Join(cfg.Root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.Root, "directory.md"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{"", "missing.md", "../escape.md", outside, "link.md", "directory.md"} {
		if _, err := g.Invoke(context.Background(), document.Source{URI: uri}); err == nil {
			t.Fatalf("accepted %q", uri)
		}
	}
	if store.Calls != 0 {
		t.Fatal("invalid path reached store")
	}
}

func TestGraphCancellationAndSharedSourceLock(t *testing.T) {
	cfg := settings(t)
	text := "# Cancellation\n" + strings.Repeat("Wait for dependency. ", 10)
	write(t, cfg, "runbook.md", text)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	store := &testutil.MemoryStore{BeforeStore: func(ctx context.Context) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}}
	g := build(t, cfg, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := g.Invoke(ctx, document.Source{URI: "runbook.md"}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("store was not called")
	}
	// Uploads and separate graphs must wait on the same source lock.
	service, err := testutil.NewKnowledgeService(cfg.Knowledge, store)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, stop := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer stop()
	if _, err := service.Ingest(waitCtx, "runbook.md", strings.NewReader(text)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("upload bypassed graph lock: %v", err)
	}
	other := build(t, cfg, store)
	if _, err := other.Invoke(waitCtx, document.Source{URI: "runbook.md"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("graph lost cancellation: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("dependency lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dependency ignored cancellation")
	}
	if store.Calls != 0 || len(store.Events) != 0 {
		t.Fatal("cancelled invocation wrote or cleaned up")
	}
	close(release)
	if _, err := g.Invoke(context.Background(), document.Source{URI: "runbook.md"}); err != nil {
		t.Fatal("lock was not released:", err)
	}
}

func TestBuildValidatesDependencies(t *testing.T) {
	cfg := settings(t)
	if _, err := knowledgepipeline.BuildKnowledgeIndexing(context.Background(), cfg, nil); err == nil {
		t.Fatal("accepted nil store")
	}
	cfg.Knowledge.BatchSize = 0
	if _, err := knowledgepipeline.BuildKnowledgeIndexing(context.Background(), cfg, &testutil.MemoryStore{}); err == nil {
		t.Fatal("accepted invalid config")
	}
}
