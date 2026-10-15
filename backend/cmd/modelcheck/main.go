package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/logic/modelcheck"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadChatModel(gctx.New())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(gctx.New(), cfg.Timeout)
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
