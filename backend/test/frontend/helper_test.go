package frontend_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/router"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
)

type memoryRetriever struct{ store *testutil.MemoryStore }

func (r memoryRetriever) Retrieve(ctx context.Context, _ string, _ ...einoretriever.Option) ([]*schema.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	docs := r.store.Snapshot()
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	if len(docs) > 3 {
		docs = docs[:3]
	}
	return docs, nil
}

// This opt-in fixture runs the real controllers and graphs with a local SDK model and memory storage.
func TestFrontendServerHelper(t *testing.T) {
	ready := os.Getenv("ONCALL_FRONTEND_HELPER_READY")
	if ready == "" {
		t.Skip("explicit frontend HTTP acceptance helper")
	}
	base := os.Getenv("ONCALL_CHAT_SIM_BASE_URL")
	if base == "" {
		t.Fatal("ONCALL_CHAT_SIM_BASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	model, err := client.NewOpenAI(ctx, config.ChatModel{BaseURL: base, Model: "fixture-model", APIKey: "fixture-only", Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	store := &testutil.PublicationMemory{}
	const max = 10485760
	cfg := config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: max, ChunkSize: 2000, ChunkOverlap: 200, BatchSize: 16}
	catalog, err := knowledge.OpenCatalog(filepath.Join(cfg.FileDir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	manager, err := knowledge.NewManager(ctx, cfg, catalog, store, "local-browser-fixture:3")
	if err != nil {
		t.Fatal(err)
	}
	search, err := knowledge.NewSearchService(catalog, store, knowledge.SearchConfig{Mode: "hybrid", TopK: 3, DenseMinScore: .5, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ONCALL_KNOWLEDGE_HELPER_SEED") == "1" {
		raw := "---\ntitle: 回源 Origin\ndocType: glossary\ndomain: cdn\nservice: cache\naliases: [Origin Fetch, origin]\ntags: [cdn]\n---\n# 回源\n\n缓存未命中时向源站获取内容。\n"
		if _, err = manager.Ingest(ctx, "cdn/origin.md", strings.NewReader(raw), knowledge.MetadataPatch{}, nil, true); err != nil {
			t.Fatal(err)
		}
	}

	graph, err := pipeline.BuildChatAgent(ctx, pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: 60 * time.Second}, TimeZone: "Asia/Shanghai"}, pipeline.Dependencies{Model: model.ToolCallingModel(), Retriever: search})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: 30 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := chatservice.NewChatService(graph, memory, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	streams, err := sse.New(chat)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeService, err := knowledge.NewManagedKnowledgeService(cfg, manager)
	if err != nil {
		t.Fatal(err)
	}

	opsService, scenario := addOpsFixture(t, ctx, &store.MemoryStore, search)
	engine := router.New(ctx, router.Dependencies{
		Chat: chat, Streams: streams, Ops: opsService, Manager: manager,
		Search: search, Upload: knowledgeService, MaxUploadBytes: max,
	})
	if scenario != nil {
		engine.Any("/__test/ops-scenario", scenario)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	if err := os.WriteFile(ready, []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}
