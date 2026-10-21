package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/modelcheck"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadEnv(); err != nil {
		return err
	}
	cfg, err := config.LoadChatModel()
	if err != nil {
		return err
	}
	// Share one total deadline across catalog lookup and completion instead of resetting it.
	ctx, logs, err := logger.Bootstrap(gctx.New(), false)
	if err != nil {
		return err
	}
	defer logs.Close()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	model, err := client.NewOpenAI(ctx, cfg)
	if err != nil {
		return err
	}
	defer model.Close()
	result, err := modelcheck.Check(ctx, model)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
