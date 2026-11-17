package router

import (
	"context"

	chatcontroller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	knowledgecontroller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/knowledge"
	uploadcontroller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

type Dependencies struct {
	Chat           *chat.ChatService
	Streams        *sse.Service
	Ops            *aiops.AIOpsService
	Manager        *knowledge.Manager
	Search         *knowledge.SearchService
	Upload         *knowledge.KnowledgeService
	MaxUploadBytes int64
}

func init() {
	// Route and request output is handled by the application logger.
	gin.SetMode(gin.ReleaseMode)
}

// New shares the production route and middleware assembly with HTTP fixtures.
func New(root context.Context, deps Dependencies) *gin.Engine {
	maxUploadBytes := deps.MaxUploadBytes
	if maxUploadBytes == 0 {
		maxUploadBytes = 10 << 20
	}
	engine := gin.New()
	engine.Use(middleware.ObserveHTTP(root), middleware.Recovery(), middleware.BodyLimit(maxUploadBytes+middleware.MultipartOverhead))

	healthAPI := health.NewV1()
	chatAPI := chatcontroller.NewV1(deps.Chat, deps.Streams)
	opsAPI := chatcontroller.NewAIOpsV1(deps.Ops)
	knowledgeAPI := knowledgecontroller.NewV1(deps.Manager, deps.Search)
	writeAPI := knowledgecontroller.NewWriteV1(deps.Manager)
	uploadAPI := uploadcontroller.NewV1(deps.Upload)

	api := engine.Group("/api")
	api.GET("/health", healthAPI.Health)
	api.POST("/chat", chatAPI.Chat)
	api.POST("/chat_stream", chatAPI.ChatStream)
	api.POST("/ai_ops", opsAPI.AIOps)
	api.POST("/ai_ops_stream", opsAPI.AIOpsStream)
	api.GET("/knowledge/documents", knowledgeAPI.List)
	api.GET("/knowledge/documents/:id", knowledgeAPI.Detail)
	api.GET("/knowledge/documents/:id/chunks", knowledgeAPI.Chunks)
	api.POST("/knowledge/search", knowledgeAPI.Search)

	files := api.Group("", middleware.UploadBody(maxUploadBytes))
	files.POST("/upload", uploadAPI.Upload)
	files.POST("/knowledge/documents", writeAPI.Create)
	files.PUT("/knowledge/documents/:id", writeAPI.Update)
	return engine
}
