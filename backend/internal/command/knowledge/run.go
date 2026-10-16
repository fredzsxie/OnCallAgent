package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	service "github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func Main(mode string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, mode, os.Stdout); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func Run(ctx context.Context, mode string, out io.Writer) error {
	if err := config.LoadEnv(); err != nil {
		return err
	}
	if mode == "milvus_check" {
		cfg, err := config.LoadMilvus(ctx)
		if err != nil {
			return err
		}
		db, err := client.NewMilvus(ctx, cfg)
		if err != nil {
			return err
		}
		defer db.Close()
		r, checkErr := service.Check(ctx, db, cfg, 0)
		if err := json.NewEncoder(out).Encode(r); err != nil {
			return err
		}
		if checkErr != nil {
			return checkErr
		}
		if !r.Ready {
			return errors.New("Milvus diagnostics completed; collection is not ready (see differences and load_state)")
		}
		return nil
	}
	if mode != "embedding_check" && mode != "init_knowledge" && mode != "knowledge_check" {
		return errors.New("unknown knowledge command")
	}
	cfg, err := config.LoadEmbedding(ctx)
	if err != nil {
		return err
	}
	emb, err := client.NewEmbedding(ctx, cfg)
	if err != nil {
		return err
	}
	defer emb.Close()
	dimension, err := service.ProbeDimension(ctx, emb)
	if err != nil {
		return err
	}
	if mode == "embedding_check" {
		return json.NewEncoder(out).Encode(map[string]any{"configured_model": cfg.Model, "dimension": dimension, "available": true})
	}
	milvusCfg, err := config.LoadMilvus(ctx)
	if err != nil {
		return err
	}
	db, err := client.NewMilvus(ctx, milvusCfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if mode == "init_knowledge" {
		r, initErr := service.Init(ctx, db, milvusCfg, dimension)
		if err := json.NewEncoder(out).Encode(r); err != nil {
			return err
		}
		return initErr
	}
	store, err := service.NewStore(ctx, db, emb, milvusCfg, dimension)
	if err != nil {
		return err
	}
	r, verifyErr := service.Verify(ctx, store)
	if err := json.NewEncoder(out).Encode(r); err != nil {
		return err
	}
	return verifyErr
}
