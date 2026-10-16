package main

import (
	"log"
	"os"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
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
	// Create one model and connection pool for all requests, rather than per controller call.
	model, err := client.NewOpenAI(ctx, cfg.ChatModel)
	if err != nil {
		return err
	}
	defer model.Close()
	chatService := chatservice.NewChatService(model)

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
	knowledgeService, err := knowledge.NewKnowledgeService(knowledgeCfg, store)
	if err != nil {
		return err
	}

	s := g.Server()
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(health.NewV1())
		group.Bind(chat.NewV1(chatService))
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
