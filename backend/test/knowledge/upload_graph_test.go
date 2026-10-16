package knowledge_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

func TestServiceInvokesGraphWithSavedFile(t *testing.T) {
	cfg := ingestionConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	text := "saved bytes 中文"
	calls := 0
	var savedURI string
	service, err := knowledge.NewKnowledgeService(cfg, func(gotCtx context.Context, src document.Source, source string) ([]string, error) {
		calls++
		if gotCtx != ctx || source != "ops/guide.MD" || !filepath.IsAbs(src.URI) {
			t.Fatal("graph input or context differs")
		}
		data, err := os.ReadFile(src.URI)
		if err != nil || string(data) != text {
			t.Fatal("graph ran before saving the complete file")
		}
		if savedURI != "" && savedURI != src.URI {
			t.Fatal("repeat changed physical path")
		}
		savedURI = src.URI
		return []string{"one", "two", "three"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := service.Ingest(ctx, "ops/guide.MD", strings.NewReader(text))
		if err != nil || result.ChunkCount != 3 || result.FileName != filepath.Base(savedURI) || result.FilePath != savedURI || result.FileSize != int64(len(text)) {
			t.Fatalf("wrong graph result: %+v %v", result, err)
		}
		info, err := os.Stat(result.FilePath)
		if err != nil || info.Size() != result.FileSize {
			t.Fatal("response does not describe the saved file")
		}
	}
	if calls != 2 {
		t.Fatal("graph was not invoked once per valid upload")
	}
}

func TestServiceRejectsInputBeforeSavingOrInvokingGraph(t *testing.T) {
	cfg := ingestionConfig(t)
	cfg.MaxUploadBytes = 8
	service, err := knowledge.NewKnowledgeService(cfg, func(context.Context, document.Source, string) ([]string, error) {
		t.Fatal("invalid input reached graph")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source, text string
		want         error
	}{
		{"a.pdf", "a", knowledge.ErrUnsupportedFormat}, {"../a.md", "a", knowledge.ErrInvalidDocument},
		{"a.txt", "", knowledge.ErrInvalidDocument}, {"a.txt", "\ufeff \n", knowledge.ErrInvalidDocument},
		{"a.txt", "\xff", knowledge.ErrInvalidDocument}, {"a.txt", "a\x00b", knowledge.ErrInvalidDocument},
		{"a.txt", "123456789", knowledge.ErrTooLarge},
	} {
		if _, err := service.Ingest(context.Background(), tc.source, strings.NewReader(tc.text)); !errors.Is(err, tc.want) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if _, err := service.Ingest(context.Background(), "a.txt", nil); !errors.Is(err, knowledge.ErrInvalidDocument) {
		t.Fatal("nil reader accepted")
	}
	if _, err := service.Ingest(context.Background(), "a.txt", errorReader{}); !errors.Is(err, knowledge.ErrSave) {
		t.Fatalf("read failure stage: %v", err)
	}
	files, err := os.ReadDir(cfg.FileDir)
	if err != nil || len(files) != 0 {
		t.Fatal("invalid input created files")
	}
}

func TestServiceKeepsSavedFileButNoChunkCountOnGraphFailure(t *testing.T) {
	for _, graphErr := range []error{errors.New("fixture-secret: graph failed"), context.DeadlineExceeded, context.Canceled, nil} {
		cfg := ingestionConfig(t)
		service, err := knowledge.NewKnowledgeService(cfg, func(context.Context, document.Source, string) ([]string, error) {
			if graphErr == nil {
				return nil, nil
			}
			return []string{"partial"}, graphErr
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Ingest(context.Background(), "failed.md", strings.NewReader("valid bytes"))
		if !errors.Is(err, knowledge.ErrIngest) || !errors.Is(err, knowledge.ErrRetryable) || result.ChunkCount != 0 {
			t.Fatalf("graph failure classified incorrectly: %+v %v", result, err)
		}
		if graphErr != nil && !errors.Is(err, graphErr) {
			t.Fatal("graph error chain lost")
		}
		if _, err := os.Stat(result.FilePath); err != nil {
			t.Fatal("graph failure removed saved version")
		}
	}
	if _, err := knowledge.NewKnowledgeService(ingestionConfig(t), nil); err == nil {
		t.Fatal("nil index function accepted")
	}
}

type holdUploadKey struct{}

func TestUploadsSerializeAcrossServicesWithoutBlockingOtherSources(t *testing.T) {
	cfg := ingestionConfig(t)
	entered, release := make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	store := &testutil.MemoryStore{BeforeStore: func(ctx context.Context) error {
		if ctx.Value(holdUploadKey{}) == true {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	first, second := ingestService(t, cfg, store), ingestService(t, cfg, store)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	defer close(release)
	done := make(chan error, 1)
	go func() {
		_, err := first.Ingest(context.WithValue(ctx, holdUploadKey{}, true), "same.md", strings.NewReader("first"))
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("graph write was not reached")
	}
	wait, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	if _, err := second.Ingest(wait, "same.md", strings.NewReader("second")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same source bypassed upload lock: %v", err)
	}
	if _, err := second.Ingest(ctx, "other.md", strings.NewReader("other")); err != nil {
		t.Fatal("different source was blocked:", err)
	}
	files, err := os.ReadDir(cfg.FileDir)
	if err != nil || len(files) != 2 {
		t.Fatal("waiting upload saved a new version")
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("upload/indexer lock deadlock")
	}
	if _, err := second.Ingest(ctx, "same.md", strings.NewReader("second")); err != nil {
		t.Fatal("retry failed:", err)
	}
	docs := store.Snapshot()
	if len(docs) != 2 {
		t.Fatalf("old version was not cleaned: %d docs", len(docs))
	}
	for _, doc := range docs {
		if doc.MetaData["source"] == "same.md" && doc.Content != "second" {
			t.Fatal("stale version survived")
		}
	}
}
