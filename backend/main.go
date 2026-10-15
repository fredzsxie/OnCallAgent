package main

import (
	"log"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	cfg, err := config.Load(gctx.New())
	if err != nil {
		log.Fatal(err)
	}

	s := g.Server()
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(health.NewV1())
	})
	s.SetPort(cfg.Port)
	s.Run()
}
