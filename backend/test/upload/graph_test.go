package upload_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

func TestUploadSaveFailureDoesNotInvokeGraph(t *testing.T) {
	occupied := filepath.Join(t.TempDir(), "fixture-secret-not-directory")
	if err := os.WriteFile(occupied, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Knowledge{FileDir: occupied, MaxUploadBytes: 128, ChunkSize: 20, ChunkOverlap: 4, BatchSize: 2}
	service, err := knowledge.NewKnowledgeService(cfg, func(context.Context, document.Source, string) ([]string, error) {
		t.Error("save failure invoked graph")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body := uploadFile(t, startAPI(t, service, cfg.MaxUploadBytes), "runbook.md", "valid document", 500)
	var failure struct {
		Stage     string
		Retryable bool
	}
	if err := json.Unmarshal(body.Data, &failure); err != nil || failure.Stage != "save" || !failure.Retryable || body.Message != "file save failed" {
		t.Fatalf("save error contract: %+v %s", body, body.Data)
	}
}

func TestUploadGraphFailureDoesNotReturnSavedFileOrChunkCount(t *testing.T) {
	cfg := config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 128, ChunkSize: 20, ChunkOverlap: 4, BatchSize: 2}
	service, err := knowledge.NewKnowledgeService(cfg, func(_ context.Context, src document.Source, logicalSource string) ([]string, error) {
		if logicalSource != "runbook.md" {
			t.Error("wrong logical source")
		}
		if _, err := os.Stat(src.URI); err != nil {
			t.Error("graph invoked before save")
		}
		return []string{"partially-written"}, errors.Join(knowledge.ErrInvalidDocument, errors.New("fixture-secret: upstream failed"))
	})
	if err != nil {
		t.Fatal(err)
	}
	body := uploadFile(t, startAPI(t, service, cfg.MaxUploadBytes), "runbook.md", "valid document", 503)
	var failure map[string]any
	if err := json.Unmarshal(body.Data, &failure); err != nil || failure["stage"] != "ingest" || failure["retryable"] != true || len(failure) != 2 {
		t.Fatalf("ingestion error exposed success fields: %s", body.Data)
	}
	files, err := os.ReadDir(cfg.FileDir)
	if err != nil || len(files) != 1 {
		t.Fatal("saved version was removed after graph failure")
	}
}

func TestUploadInvalidRequestsDoNotInvokeGraph(t *testing.T) {
	cfg := config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 64, ChunkSize: 20, ChunkOverlap: 4, BatchSize: 2}
	service, err := knowledge.NewKnowledgeService(cfg, func(context.Context, document.Source, string) ([]string, error) {
		t.Error("invalid upload invoked graph")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	api := startAPI(t, service, cfg.MaxUploadBytes)
	for _, tc := range []struct {
		name, text string
		status     int
	}{
		{"a.txt", "", 400}, {"a.md", " \n", 400}, {"a.md", "\ufeff \n", 400},
		{"a.pdf", "hello", 415}, {"a.txt", strings.Repeat("x", 65), 413},
	} {
		uploadFile(t, api, tc.name, tc.text, tc.status)
	}
	files, err := os.ReadDir(cfg.FileDir)
	if err != nil || len(files) != 0 {
		t.Fatal("invalid upload saved files")
	}
}

func TestUploadCancelledGraphHasRequestCancellationStatus(t *testing.T) {
	cfg := config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 128, ChunkSize: 20, ChunkOverlap: 4, BatchSize: 2}
	service, err := knowledge.NewKnowledgeService(cfg, func(context.Context, document.Source, string) ([]string, error) { return nil, context.Canceled })
	if err != nil {
		t.Fatal(err)
	}
	body := uploadFile(t, startAPI(t, service, cfg.MaxUploadBytes), "runbook.md", "valid", 408)
	if body.Message != "request cancelled" || string(body.Data) != "null" {
		t.Fatalf("cancellation contract: %+v", body)
	}
}
