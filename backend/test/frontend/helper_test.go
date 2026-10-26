package frontend_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"testing"
	"time"

	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	chatcontroller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
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
	store := &testutil.MemoryStore{}
	graph, err := pipeline.BuildChatAgent(ctx, pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: 60 * time.Second}, TimeZone: "Asia/Shanghai"}, pipeline.Dependencies{Model: model.ToolCallingModel(), Retriever: memoryRetriever{store}})
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
	const max = 10485760
	knowledge, err := testutil.NewKnowledgeService(config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: max, ChunkSize: 2000, ChunkOverlap: 200, BatchSize: 16}, store)
	if err != nil {
		t.Fatal(err)
	}
	server := ghttp.GetServer("frontend-test-" + guid.S())
	server.SetDumpRouterMap(false)
	server.SetAccessLogEnabled(false)
	server.SetErrorLogEnabled(false)
	server.SetClientMaxBodySize(max + middleware.MultipartOverhead)
	opsService := addOpsFixture(t, ctx, server, store)
	server.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(chatcontroller.NewV1(chat, streams))
		if opsService != nil {
			group.Bind(chatcontroller.NewAIOpsV1(opsService))
		}
		group.Group("", func(files *ghttp.RouterGroup) {
			files.Middleware(middleware.UploadBody(max))
			files.Bind(upload.NewV1(knowledge))
		})
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetListener(listener); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Shutdown(); err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	if err := os.WriteFile(ready, []byte("http://"+listener.Addr().String()), 0600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}
