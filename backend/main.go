package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	chatpipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	knowledgepipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	// Populate the environment before configuration applies its override rules.
	if err := config.LoadEnv(); err != nil {
		return err
	}
	ctx := gctx.New()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	agentCfg, err := config.LoadChatAgent()
	if err != nil {
		return err
	}
	memoryCfg, err := config.LoadChatMemory()
	if err != nil {
		return err
	}
	retrievalCfg, err := config.LoadRetrieval()
	if err != nil {
		return err
	}
	toolsCfg, err := config.LoadTools()
	if err != nil {
		return err
	}

	// Create one model and connection pool for all requests, rather than per controller call.
	model, err := client.NewOpenAI(ctx, cfg.ChatModel)
	if err != nil {
		return err
	}
	defer model.Close()

	knowledgeCfg, err := config.LoadKnowledge()
	if err != nil {
		return err
	}
	embeddingCfg, err := config.LoadEmbedding()
	if err != nil {
		return err
	}
	milvusCfg, err := config.LoadMilvus()
	if err != nil {
		return err
	}
	emb, err := client.NewEmbedding(ctx, embeddingCfg)
	if err != nil {
		return err
	}
	defer emb.Close()
	db, err := client.NewMilvus(ctx, milvusCfg)
	if err != nil {
		return err
	}
	defer db.Close()
	store, err := knowledge.NewStore(ctx, db, emb, milvusCfg)
	if err != nil {
		return err
	}
	recall, err := retriever.NewMilvusRetriever(store, retrievalCfg)
	if err != nil {
		return err
	}
	registry, err := tools.NewRegistry(toolsCfg, recall, time.Now)
	if err != nil {
		return err
	}
	chatGraph, err := chatpipeline.BuildChatAgent(ctx, chatpipeline.Config{Agent: agentCfg, TimeZone: toolsCfg.TimeZone}, chatpipeline.Dependencies{Model: model.ToolCallingModel(), Retriever: recall, Tools: registry.Tools()})
	if err != nil {
		return err
	}
	memory, err := mem.NewStore(memoryCfg, nil)
	if err != nil {
		return err
	}
	chatService, err := chatservice.NewChatService(chatGraph, memory, agentCfg.Timeout)
	if err != nil {
		return err
	}
	streams, err := sse.New(chatService)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(knowledgeCfg.FileDir, 0700); err != nil {
		return err
	}
	knowledgeCfg.FileDir, err = filepath.EvalSymlinks(knowledgeCfg.FileDir)
	if err != nil {
		return err
	}
	graph, err := knowledgepipeline.BuildKnowledgeIndexing(ctx, knowledgepipeline.Config{Root: knowledgeCfg.FileDir, Knowledge: knowledgeCfg}, store)
	if err != nil {
		return err
	}
	knowledgeService, err := knowledge.NewKnowledgeService(knowledgeCfg, knowledgepipeline.NewFileIndexer(graph))
	if err != nil {
		return err
	}

	s := g.Server()
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(health.NewV1())
		group.Bind(chat.NewV1(chatService, streams))
		group.Group("", func(files *ghttp.RouterGroup) {
			files.Middleware(middleware.UploadBody(knowledgeCfg.MaxUploadBytes))
			files.Bind(upload.NewV1(knowledgeService))
		})
	})
	s.SetClientMaxBodySize(knowledgeCfg.MaxUploadBytes + middleware.MultipartOverhead)
	s.SetPort(cfg.Port)
	s.Run()
	return nil
}
