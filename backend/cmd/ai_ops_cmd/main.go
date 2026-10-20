package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

var errInitialize = errors.New("could not initialize ops dependencies; check enabled tools and model configuration")

func main() {
	result, err := run()
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		detail := []pipeline.Event{}
		if result != nil {
			detail = result.Detail
		}
		code := pipeline.ErrorCode(err)
		var failure *aiops.Error
		if errors.As(err, &failure) {
			code = failure.Code
		}
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"error": map[string]string{"code": code, "message": err.Error()}, "detail": detail})
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(1)
	}
}

func run() (*pipeline.Result, error) {
	f := flag.NewFlagSet("ai_ops_cmd", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	query := f.String("query", "", "operations objective (required)")
	eventPath := f.String("events", "", "write summarized JSONL events to a new file")
	if err := f.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			f.SetOutput(os.Stderr)
			f.PrintDefaults()
			return nil, err
		}
		return nil, pipeline.ErrInvalidInput
	}
	if strings.TrimSpace(*query) == "" || f.NArg() != 0 {
		return nil, pipeline.ErrInvalidInput
	}
	if err := config.LoadEnv(); err != nil {
		return nil, errInitialize
	}
	cfg, err := config.LoadOpsAgent()
	if err != nil {
		return nil, errInitialize
	}
	plannerCfg, executorCfg, err := config.LoadOpsModels()
	if err != nil {
		return nil, errInitialize
	}
	toolCfg, err := config.LoadTools()
	if err != nil {
		return nil, errInitialize
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	startupError := func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errInitialize
	}
	var sink func(pipeline.Event) error
	if *eventPath != "" {
		file, err := os.OpenFile(*eventPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, pipeline.ErrEventWrite
		}
		defer file.Close()
		encoder := json.NewEncoder(file)
		sink = func(e pipeline.Event) error { return encoder.Encode(e) }
	}
	planner, err := client.NewOpenAI(ctx, plannerCfg)
	if err != nil {
		return nil, startupError()
	}
	defer planner.Close()
	executor, err := client.NewOpenAI(ctx, executorCfg)
	if err != nil {
		return nil, startupError()
	}
	defer executor.Close()
	ops, err := client.OpenOperations(ctx, toolCfg)
	if err != nil {
		return nil, startupError()
	}
	defer ops.Close()
	deps := tools.Dependencies{Prometheus: ops.Prometheus, Logs: ops.Logs}
	if toolCfg.QueryInternalDocsEnabled {
		embCfg, err := config.LoadEmbedding()
		if err != nil {
			return nil, startupError()
		}
		dbCfg, err := config.LoadMilvus()
		if err != nil {
			return nil, startupError()
		}
		emb, err := client.NewEmbedding(ctx, embCfg)
		if err != nil {
			return nil, startupError()
		}
		defer emb.Close()
		db, err := client.NewMilvus(ctx, dbCfg)
		if err != nil {
			return nil, startupError()
		}
		defer db.Close()
		store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
		if err != nil {
			return nil, startupError()
		}
		rCfg, err := config.LoadRetrieval()
		if err != nil {
			return nil, startupError()
		}
		deps.Retriever, err = retriever.NewMilvusRetriever(store, rCfg)
		if err != nil {
			return nil, startupError()
		}
	}
	registry, err := tools.NewRegistry(toolCfg, deps)
	if err != nil {
		return nil, startupError()
	}
	agent, err := pipeline.BuildOpsAgent(ctx, cfg, pipeline.Dependencies{PlannerModel: planner.ToolCallingModel(), ExecutorModel: executor.ToolCallingModel(), Tools: registry.Tools()})
	if err != nil {
		return nil, startupError()
	}
	service, err := aiops.New(agent, cfg.Timeout)
	if err != nil {
		return nil, startupError()
	}
	return service.RunWithEvents(ctx, *query, sink)
}
