package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
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
	embeddingCfg, err := config.LoadEmbedding()
	if err != nil {
		return err
	}
	milvusCfg, err := config.LoadMilvus()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, logs, err := logger.Bootstrap(ctx, false)
	if err != nil {
		return err
	}
	defer logs.Close()
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
	dimension, err := knowledge.Init(ctx, db, emb, milvusCfg)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"database": milvusCfg.Database, "collection": milvusCfg.Collection, "dimension": dimension, "ready": true})
}
