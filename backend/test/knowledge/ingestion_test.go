package knowledge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

func ingestionConfig(t *testing.T) config.Knowledge {
	t.Helper()
	return config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 1 << 20, ChunkSize: 20, ChunkOverlap: 4, BatchSize: 2}
}
func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func docIDs(store *testutil.MemoryStore) []string {
	ids := []string{}
	for _, doc := range store.Snapshot() {
		ids = append(ids, doc.ID)
	}
	sort.Strings(ids)
	return ids
}
func ingestService(t *testing.T, cfg config.Knowledge, store *testutil.MemoryStore) *knowledge.KnowledgeService {
	t.Helper()
	service, err := knowledge.NewKnowledgeService(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSplitterUTF8TitlesAndFences(t *testing.T) {
	cfg := ingestionConfig(t)
	text := "# 标题一\n" + strings.Repeat("中文🙂内容。", 20) + "\n## 标题二\n第二部分"
	docs, err := knowledge.Split("folder/指南.md", text, hashText(text), cfg)
	if err != nil || len(docs) < 3 {
		t.Fatalf("split failed: %v", err)
	}
	for i, doc := range docs {
		if !utf8.ValidString(doc.Content) || len(doc.Content) > cfg.ChunkSize || len(doc.Content) > 65535 || strings.TrimSpace(doc.Content) == "" {
			t.Fatal("invalid UTF-8 chunk")
		}
		if doc.MetaData["chunk_index"] != i || doc.MetaData["content_hash"] != hashText(doc.Content) || doc.MetaData["source"] != "folder/指南.md" {
			t.Fatal("invalid chunk metadata")
		}
	}
	cfg.ChunkSize = 1000
	docs, err = knowledge.Split("x.md", "# Visible\n```md\n# Hidden\n```\n## Next\nbody", "hash", cfg)
	if err != nil || len(docs) != 2 || docs[0].MetaData["title"] != "Visible" || docs[1].MetaData["title"] != "Next" || !strings.Contains(docs[0].Content, "# Hidden") {
		t.Fatalf("fence handling failed: %v", err)
	}
	cfg.ChunkSize = 4
	cfg.ChunkOverlap = 3
	docs, err = knowledge.Split("x.txt", "🙂🙂🙂", "hash", cfg)
	if err != nil || len(docs) != 3 {
		t.Fatalf("overlap must make progress at UTF-8 boundaries: %v", err)
	}
	cfg.ChunkSize = 65535
	cfg.ChunkOverlap = 0
	text = strings.Repeat("🙂", 20000)
	docs, err = knowledge.Split("long.txt", text, "hash", cfg)
	joined := ""
	for _, doc := range docs {
		joined += doc.Content
	}
	if err != nil || joined != text {
		t.Fatalf("long line lost data: %v", err)
	}
	for _, text := range []string{"", " \t\n", "\uFEFF", string([]byte{0xff}), "a\x00b"} {
		if _, err := knowledge.Split("x.txt", text, "hash", cfg); !errors.Is(err, knowledge.ErrInvalidDocument) {
			t.Fatal("invalid or empty text accepted")
		}
	}
}

func TestIngestIdempotencyAndUpdate(t *testing.T) {
	cfg := ingestionConfig(t)
	store := &testutil.MemoryStore{}
	service := ingestService(t, cfg, store)
	text := "# Title\n" + strings.Repeat("original 内容🙂 ", 20)
	first, err := service.Ingest(context.Background(), "guide.MD", strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	ids := docIDs(store)
	second, err := service.Ingest(context.Background(), "guide.MD", strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !reflect.DeepEqual(ids, docIDs(store)) || len(ids) != first.ChunkCount {
		t.Fatal("duplicate upload changed IDs or saved path")
	}
	data, err := os.ReadFile(first.FilePath)
	if err != nil || string(data) != text || first.FileSize != int64(len(data)) || !filepath.IsAbs(first.FilePath) {
		t.Fatal("saved path or size is incorrect")
	}
	if strings.Contains(first.FileName, "guide") || first.ContentHash != hashText(text) {
		t.Fatal("unsafe saved filename or hash")
	}
	updated, err := service.Ingest(context.Background(), "guide.MD", strings.NewReader("# New\nshort version"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.FilePath == first.FilePath || len(store.Snapshot()) != updated.ChunkCount {
		t.Fatal("old version was not cleaned")
	}
	for _, doc := range store.Snapshot() {
		if doc.MetaData["document_hash"] != updated.ContentHash {
			t.Fatal("old document survived")
		}
	}
	if store.Events[len(store.Events)-1] != "delete" {
		t.Fatal("cleanup must happen last")
	}
	for _, size := range store.BatchSizes {
		if size > cfg.BatchSize {
			t.Fatal("batch limit exceeded")
		}
	}
}

func TestPartialWriteAndCleanupFailuresConvergeOnRetry(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		cfg := ingestionConfig(t)
		store := &testutil.MemoryStore{}
		service := ingestService(t, cfg, store)
		if _, err := service.Ingest(context.Background(), "guide.md", strings.NewReader("old content")); err != nil {
			t.Fatal(err)
		}
		oldIDs := docIDs(store)
		if cleanupFailure {
			store.FailDelete = true
		} else {
			store.FailBatch = store.Calls + 2
		}
		text := strings.Repeat("new content 中文🙂 ", 20)
		if _, err := service.Ingest(context.Background(), "guide.md", strings.NewReader(text)); !errors.Is(err, knowledge.ErrRetryable) {
			t.Fatal("failure must be retryable")
		}
		current := docIDs(store)
		for _, id := range oldIDs {
			if i := sort.SearchStrings(current, id); i == len(current) || current[i] != id {
				t.Fatal("old content was deleted before success")
			}
		}
		store.FailDelete = false
		store.FailBatch = 0
		result, err := service.Ingest(context.Background(), "guide.md", strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		if len(store.Snapshot()) != result.ChunkCount {
			t.Fatal("retry did not converge to the new version")
		}
	}
}

func TestInvalidUploadsAndReadErrors(t *testing.T) {
	cfg := ingestionConfig(t)
	cfg.MaxUploadBytes = 8
	store := &testutil.MemoryStore{}
	service := ingestService(t, cfg, store)
	for _, tt := range []struct {
		source, content string
		want            error
	}{
		{"x.pdf", "a", knowledge.ErrUnsupportedFormat}, {"../x.md", "a", knowledge.ErrInvalidDocument},
		{"x.txt", "", knowledge.ErrInvalidDocument}, {"x.txt", "  \n", knowledge.ErrInvalidDocument},
		{"x.txt", "123456789", knowledge.ErrTooLarge}, {"x.txt", string([]byte{0xff}), knowledge.ErrInvalidDocument},
	} {
		if _, err := service.Ingest(context.Background(), tt.source, strings.NewReader(tt.content)); !errors.Is(err, tt.want) {
			t.Fatalf("%q: got %v", tt.source, err)
		}
	}
	if _, err := service.Ingest(context.Background(), "x.txt", errorReader{}); !errors.Is(err, knowledge.ErrRetryable) {
		t.Fatal("read failure must propagate")
	}
	store.BadIDs = true
	if _, err := service.Ingest(context.Background(), "x.txt", strings.NewReader("valid")); !errors.Is(err, knowledge.ErrRetryable) {
		t.Fatal("incomplete store result accepted")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSourceLockHonorsCancellation(t *testing.T) {
	cfg := ingestionConfig(t)
	store := &testutil.MemoryStore{}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.BeforeStore = func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	service := ingestService(t, cfg, store)
	done := make(chan error, 1)
	go func() {
		_, err := service.Ingest(context.Background(), "x.txt", strings.NewReader("first"))
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := service.Ingest(ctx, "x.txt", strings.NewReader("second")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting for source lock ignored cancellation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSavedDirectoryFailureCannotWriteVectors(t *testing.T) {
	cfg := ingestionConfig(t)
	file := filepath.Join(cfg.FileDir, "not-a-directory")
	if err := os.WriteFile(file, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.FileDir = file
	store := &testutil.MemoryStore{}
	service := ingestService(t, cfg, store)
	if _, err := service.Ingest(context.Background(), "x.txt", strings.NewReader("valid")); !errors.Is(err, knowledge.ErrRetryable) || store.Calls != 0 {
		t.Fatal("save failure must stop ingestion")
	}
}

func TestSplitterVersionUsesSplitParametersOnly(t *testing.T) {
	cfg := ingestionConfig(t)
	text := "# " + strings.Repeat("标题🙂", 300) + "\nbody"
	first, err := knowledge.Split("x.md", text, hashText(text), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range first {
		title := doc.MetaData["title"].(string)
		if len(title) > 1024 || !utf8.ValidString(title) {
			t.Fatal("title metadata is not bounded UTF-8")
		}
	}
	cfg.BatchSize++
	second, err := knowledge.Split("x.md", text, hashText(text), cfg)
	if err != nil || first[0].ID != second[0].ID {
		t.Fatal("batch size must not affect stable IDs")
	}
	cfg.ChunkSize++
	third, err := knowledge.Split("x.md", text, hashText(text), cfg)
	if err != nil || first[0].ID == third[0].ID {
		t.Fatal("split settings must participate in the version")
	}
}
