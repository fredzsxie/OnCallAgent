package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/cloudwego/eino-ext/devops"
	"github.com/cloudwego/eino/components/document"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge/pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	file := flag.String("file", "", "txt/md/markdown path inside root")
	root := flag.String("root", ".", "source root directory")
	dev := flag.Bool("dev", false, "register the graph with Eino Dev and wait for signals")
	port := flag.Int("dev-port", 52538, "Eino Dev server port")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if !*dev && *file == "" {
		return errors.New("--file is required unless --dev is enabled")
	}
	if *port < 1 || *port > 65535 {
		return errors.New("--dev-port must be between 1 and 65535")
	}
	if err := config.LoadEnv(); err != nil {
		return err
	}
	cfg, err := config.LoadKnowledge()
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	// DevOps installs compile callbacks, so it must be initialized before building the graph.
	if *dev {
		if err := devops.Init(ctx, devops.WithDevServerPort(strconv.Itoa(*port))); err != nil {
			return err
		}
	}
	graph, err := pipeline.BuildKnowledgeIndexing(ctx, pipeline.Config{Root: *root, Knowledge: cfg}, store)
	if err != nil {
		return err
	}
	if *file != "" {
		ids, err := graph.Invoke(ctx, document.Source{URI: *file})
		if err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(ids); err != nil {
			return err
		}
	}
	if *dev {
		fmt.Fprintf(os.Stderr, "KnowledgeIndexing registered on port %d; waiting for SIGINT/SIGTERM\n", *port)
		<-ctx.Done()
	}
	return nil
}
