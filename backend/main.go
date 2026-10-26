package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	chatpipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	knowledgepipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	opspipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	if err := run(); err != nil {
		log.Print("服务启动失败：请检查配置及应用日志")
		os.Exit(1)
	}
}

func run() (err error) {
	// Populate the environment before configuration applies its override rules.
	if err := config.LoadEnv(); err != nil {
		log.Print(err)
		return err
	}
	ctx, logs, err := logger.Bootstrap(gctx.New(), true)
	if err != nil {
		log.Print(err)
		return err
	}
	defer logs.Close()
	stage := "configuration"
	defer func() {
		if err != nil {
			logger.Failure(ctx, "App", "服务启动或运行失败", err, logger.F("stage", stage))
		} else {
			logger.Info(ctx, "App", "服务已停止")
		}
	}()
	logger.Info(ctx, "App", "开始初始化服务")
	cfg, err := config.Load()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	agentCfg, err := config.LoadChatAgent()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	memoryCfg, err := config.LoadChatMemory()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	retrievalCfg, err := config.LoadRetrieval()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	toolsCfg, err := config.LoadTools()
	if err != nil {
		return configurationFailure(ctx, err)
	}

	// Create one model and connection pool for all requests, rather than per controller call.
	stage = "chat_model"
	model, err := client.NewOpenAI(ctx, cfg.ChatModel)
	if err != nil {
		return err
	}
	defer model.Close()

	knowledgeCfg, err := config.LoadKnowledge()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	embeddingCfg, err := config.LoadEmbedding()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	milvusCfg, err := config.LoadMilvus()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	stage = "embedding"
	emb, err := client.NewEmbedding(ctx, embeddingCfg)
	if err != nil {
		return err
	}
	defer emb.Close()
	stage = "milvus"
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
	stage = "tools"
	operations, err := client.OpenOperations(ctx, toolsCfg)
	if err != nil {
		return err
	}
	defer operations.Close()
	registry, err := tools.NewRegistry(toolsCfg, tools.Dependencies{Retriever: recall, Now: time.Now, Prometheus: operations.Prometheus})
	if err != nil {
		return err
	}
	opsCfg, err := config.LoadOpsAgent()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	plannerCfg, executorCfg, err := config.LoadOpsModels()
	if err != nil {
		return configurationFailure(ctx, err)
	}
	planner, err := client.NewOpenAI(ctx, plannerCfg, "planner")
	if err != nil {
		return err
	}
	defer planner.Close()
	executor, err := client.NewOpenAI(ctx, executorCfg, "executor")
	if err != nil {
		return err
	}
	defer executor.Close()
	stage = "ops_graph"
	opsAgent, err := opspipeline.BuildOpsAgent(ctx, opsCfg, opspipeline.Dependencies{PlannerModel: planner.ToolCallingModel(), ExecutorModel: executor.ToolCallingModel(), Tools: registry.Tools()})
	if err != nil {
		return err
	}
	opsService, err := aiops.New(opsAgent, opsCfg.Timeout)
	if err != nil {
		return err
	}
	stage = "chat_graph"
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
	stage = "knowledge_graph"
	graph, err := knowledgepipeline.BuildKnowledgeIndexing(ctx, knowledgepipeline.Config{Root: knowledgeCfg.FileDir, Knowledge: knowledgeCfg}, store)
	if err != nil {
		return err
	}
	knowledgeService, err := knowledge.NewKnowledgeService(knowledgeCfg, knowledgepipeline.NewFileIndexer(graph))
	if err != nil {
		return err
	}

	s := g.Server()
	middleware.ObserveHTTP(s, ctx)
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(health.NewV1())
		group.Bind(chat.NewV1(chatService, streams))
		group.Bind(chat.NewAIOpsV1(opsService))
		group.Group("", func(files *ghttp.RouterGroup) {
			files.Middleware(middleware.UploadBody(knowledgeCfg.MaxUploadBytes))
			files.Bind(upload.NewV1(knowledgeService))
		})
	})
	s.SetClientMaxBodySize(knowledgeCfg.MaxUploadBytes + middleware.MultipartOverhead)
	s.SetPort(cfg.Port)
	stage = "http_listen"
	if err = s.Start(); err != nil {
		return errors.New("cannot start HTTP server; check listen port")
	}
	middleware.PrintRoutes(ctx, s)
	logger.Info(ctx, "Tool", "工具注册完成", logger.F("docs_enabled", toolsCfg.QueryInternalDocsEnabled), logger.F("time_enabled", toolsCfg.GetCurrentTimeEnabled), logger.F("alerts_enabled", toolsCfg.QueryPrometheusAlertsEnabled))
	logger.Info(ctx, "App", "HTTP 服务已启动", logger.F("port", cfg.Port))
	stage = "serving"
	ghttp.Wait()
	return nil
}

// Configuration loaders return validation messages without interpolating configured values.
func configurationFailure(ctx context.Context, err error) error {
	logger.Error(ctx, "Config", "配置校验失败", logger.F("error_type", "invalid_configuration"), logger.F("reason", err.Error()))
	return err
}
