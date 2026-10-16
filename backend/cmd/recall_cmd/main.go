package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("recall_cmd", flag.ContinueOnError)
	query := flags.String("query", "", "question to retrieve from the knowledge base")
	list := flags.Bool("list-tools", false, "print enabled tool input/output schemas without connecting to dependencies")
	name := flags.String("tool", "", "enabled tool name to invoke")
	input := flags.String("input", "{}", "JSON arguments for --tool")
	topK := flags.Int("top-k", 3, "override retrieval top k")
	threshold := flags.Float64("score-threshold", 0.5, "override minimum COSINE score")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	modes := 0
	if set["query"] {
		modes++
	}
	if *list {
		modes++
	}
	if set["tool"] {
		modes++
	}
	if modes != 1 || flags.NArg() != 0 {
		return errors.New("choose exactly one of --query, --list-tools or --tool")
	}
	if set["query"] && strings.TrimSpace(*query) == "" {
		return retriever.ErrInvalidQuery
	}
	if set["input"] && !set["tool"] {
		return errors.New("--input requires --tool")
	}
	if set["tool"] && !json.Valid([]byte(*input)) {
		return errors.New("--input must be valid JSON")
	}
	needsDocs := set["query"] || *name == tools.QueryInternalDocs
	if !needsDocs && (set["top-k"] || set["score-threshold"]) {
		return errors.New("retrieval flags require a retrieval operation")
	}
	if err := config.LoadEnv(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var toolsCfg config.Tools
	if *list || set["tool"] {
		var err error
		toolsCfg, err = config.LoadTools()
		if err != nil {
			return err
		}
		if *list {
			definitions, err := tools.Definitions(toolsCfg)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(definitions)
		}
		enabled, err := tools.Enabled(toolsCfg, *name)
		if err != nil {
			return err
		}
		if !enabled {
			return tools.ErrDisabledTool
		}
		// Build only the selected tool; a time-only invocation needs no cloud credentials.
		toolsCfg.QueryInternalDocsEnabled = *name == tools.QueryInternalDocs
		toolsCfg.GetCurrentTimeEnabled = *name == tools.GetCurrentTime
	}
	var r *retriever.MilvusRetriever
	if needsDocs {
		cfg, err := config.LoadRetrieval()
		if err != nil {
			return err
		}
		if set["top-k"] {
			cfg.TopK = *topK
		}
		if set["score-threshold"] {
			cfg.ScoreThreshold = *threshold
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		embCfg, err := config.LoadEmbedding()
		if err != nil {
			return err
		}
		dbCfg, err := config.LoadMilvus()
		if err != nil {
			return err
		}
		emb, err := client.NewEmbedding(ctx, embCfg)
		if err != nil {
			return err
		}
		defer emb.Close()
		db, err := client.NewMilvus(ctx, dbCfg)
		if err != nil {
			return err
		}
		defer db.Close()
		store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
		if err != nil {
			return err
		}
		r, err = retriever.NewMilvusRetriever(store, cfg)
		if err != nil {
			return err
		}
	}
	if set["query"] {
		result, err := r.Search(ctx, *query)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	registry, err := tools.NewRegistry(toolsCfg, r, time.Now)
	if err != nil {
		return err
	}
	output, err := registry.Invoke(ctx, *name, *input)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(json.RawMessage(output))
}
