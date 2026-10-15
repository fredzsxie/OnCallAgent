package main

import (
	"log"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	// Populate the environment before configuration applies its override rules.
	if err := config.LoadEnv(); err != nil {
		log.Fatal(err)
	}
	ctx := gctx.New()
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// Create one model and connection pool for all requests, rather than per controller call.
	model, err := client.NewOpenAI(ctx, cfg.ChatModel)
	if err != nil {
		log.Fatal(err)
	}
	defer model.Close()
	chatService := chatservice.NewChatService(model)

	s := g.Server()
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(health.NewV1())
		group.Bind(chat.NewV1(chatService))
	})
	s.SetPort(cfg.Port)
	s.Run()
}
