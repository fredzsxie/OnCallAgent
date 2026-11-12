package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

type legacyMemory struct {
	docs  []*schema.Document
	calls int
}

var boundPattern = regexp.MustCompile(`id (>=|<) ("(?:[^"\\]|\\.)*")`)

func (l *legacyMemory) Query(ctx context.Context, collection, filter string, fields []string, limit, offset int) (milvusclient.DataSet, error) {
	l.calls++
	ids, contents, metas := []string{}, []string{}, [][]byte{}
	for i := len(l.docs) - 1; i >= 0; i-- {
		d := l.docs[i]
		match := true
		for _, part := range boundPattern.FindAllStringSubmatch(filter, -1) {
			var pivot string
			if err := json.Unmarshal([]byte(part[2]), &pivot); err != nil {
				return nil, err
			}
			if part[1] == ">=" && d.ID < pivot || part[1] == "<" && d.ID >= pivot {
				match = false
			}
		}
		if !match {
			continue
		}
		raw, _ := json.Marshal(d.MetaData)
		ids = append(ids, d.ID)
		contents = append(contents, d.Content)
		metas = append(metas, raw)
		if len(ids) == limit {
			break
		}
	}
	return milvusclient.DataSet{column.NewColumnVarChar("id", ids), column.NewColumnVarChar("content", contents), column.NewColumnJSONBytes("metadata", metas)}, ctx.Err()
}
func TestMigrationScansUnorderedRangesAndResumes(t *testing.T) {
	ctx := context.Background()
	source := &legacyMemory{}
	for i := 0; i < 1300; i++ {
		source.docs = append(source.docs, &schema.Document{ID: fmt.Sprintf("row-%04d", i), Content: fmt.Sprintf("CDN term %d", i), MetaData: map[string]any{"source": "legacy.md", "version": "v1", "chunk_index": i}})
	}
	cfg := ingestionConfig(t)
	plan, err := knowledge.InspectLegacy(ctx, source, "old", cfg.FileDir, nil)
	if err != nil || plan.SourceChunks != 1300 || len(plan.Documents) != 1 || source.calls < 3 || !plan.Documents[0].IndexOnly {
		t.Fatalf("incomplete scan: %+v calls=%d %v", plan, source.calls, err)
	}
	c, err := knowledge.OpenCatalog(filepath.Join(cfg.FileDir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	store := &publicationMemory{}
	store.FailBatch = 2
	m, err := knowledge.NewManager(ctx, cfg, c, store, "target:model:3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = knowledge.ApplyMigration(ctx, m, plan); err == nil {
		t.Fatal("partial write unexpectedly published")
	}
	page, _ := c.List(ctx, knowledge.Filters{}, "", 1, 20)
	if page.Items[0].ActiveVersion != "" {
		t.Fatal("partial migration published")
	}
	store.FailBatch = 0
	count, err := knowledge.ApplyMigration(ctx, m, plan)
	if err != nil || count != 1 || len(store.Snapshot()) != 1300 {
		t.Fatalf("resume failed: %d %v", count, err)
	}
	count, err = knowledge.ApplyMigration(ctx, m, plan)
	if err != nil || count != 1 || len(store.Snapshot()) != 1300 {
		t.Fatal("repeat migration was not idempotent")
	}
	page, _ = c.List(ctx, knowledge.Filters{}, "", 1, 20)
	detail, err := m.Detail(ctx, page.Items[0].ID)
	if err != nil || !detail.IndexOnly || detail.Content != "" {
		t.Fatal("missing original was reconstructed as a full document")
	}
	source.docs = append(source.docs, &schema.Document{ID: "later", Content: "later version", MetaData: map[string]any{"source": "legacy.md", "version": "v2"}})
	conflict, err := knowledge.InspectLegacy(ctx, source, "old", cfg.FileDir, nil)
	if err != nil || conflict.Conflicts != 1 || conflict.SnapshotHash == plan.SnapshotHash {
		t.Fatal("version conflict or changed snapshot not detected")
	}
	if _, err = knowledge.ApplyMigration(ctx, m, conflict); !errors.Is(err, knowledge.ErrConflict) {
		t.Fatal("ambiguous version applied")
	}
	selected, err := knowledge.InspectLegacy(ctx, source, "old", cfg.FileDir, map[string]string{"legacy.md": "v1"})
	if err != nil || selected.Conflicts != 0 || selected.Documents[0].ChunkCount != 1300 {
		t.Fatal("explicit selection failed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = knowledge.InspectLegacy(cancelled, source, "old", strings.Repeat("x", 2), nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}
