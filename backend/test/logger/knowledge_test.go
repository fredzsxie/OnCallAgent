package logger_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func TestKnowledgeGraphStagesAndSafeFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed"}[fail], func(t *testing.T) {
			cfg := config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 4096, ChunkSize: 40, ChunkOverlap: 5, BatchSize: 2}
			store := &testutil.MemoryStore{}
			if fail {
				store.FailBatch = 1
			}
			service, err := testutil.NewKnowledgeService(cfg, store)
			if err != nil {
				t.Fatal(err)
			}
			var out capture
			ctx, _ := testLogger(t, settings(), logger.Options{Console: &out})
			ctx, id := logger.WithRequest(ctx)
			_, err = service.Ingest(ctx, "private-name.md", strings.NewReader("# Private title\n\nprivate-document-content text and enough words to split into chunks."))
			if (err != nil) != fail {
				t.Fatal("wrong result", err)
			}
			lines := requestLines(out.String(), id)
			for _, want := range []string{"文件保存完成", "文件读取完成", "文档切分完成", "开始写入批次"} {
				if !strings.Contains(lines, want) {
					t.Fatal("missing stage", want, lines)
				}
			}
			if fail {
				if !strings.Contains(lines, "stage=ingest") || !strings.Contains(lines, "文件处理失败") {
					t.Fatal(lines)
				}
			} else {
				if !strings.Contains(lines, "批次写入已确认") || !strings.Contains(lines, "文件入库完成") {
					t.Fatal(lines)
				}
			}
			for _, secret := range []string{cfg.FileDir, "private-name.md", "Private title", "private-document-content", "fixture-secret"} {
				if strings.Contains(out.String(), secret) {
					t.Fatal("knowledge data leaked", secret)
				}
			}
		})
	}
	// Logging must remain optional for code and tests that do not install a logger.
	logger.Info(context.Background(), "Test", "不会输出")
}
