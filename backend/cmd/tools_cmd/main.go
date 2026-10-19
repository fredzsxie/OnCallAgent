package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		var failure *operations.Error
		if !errors.As(err, &failure) {
			failure = operations.Failure("tools_cmd", operations.ErrInvalidInput)
			if errors.Is(err, tools.ErrDisabledTool) {
				failure.Code, failure.Message = "disabled_tool", "tool is disabled"
			}
			if errors.Is(err, tools.ErrUnknownTool) {
				failure.Code, failure.Message = "unknown_tool", "unknown tool"
			}
		}
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"error": failure})
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("tools_cmd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	list := flags.Bool("list-tools", false, "print enabled local tool input/output schemas without connections")
	mcpTools := flags.Bool("mcp-tools", false, "initialize configured CLS MCP and inspect its tool schemas")
	name := flags.String("tool", "", "enabled tool name to invoke")
	input := flags.String("input", "{}", "JSON arguments for --tool")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stderr)
			fmt.Fprintln(os.Stderr, "Usage of tools_cmd:")
			flags.PrintDefaults()
		}
		return err
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	modes := 0
	if *list {
		modes++
	}
	if *mcpTools {
		modes++
	}
	if set["tool"] {
		modes++
	}
	if modes != 1 || flags.NArg() != 0 || (set["input"] && !set["tool"]) {
		return operations.Failure("tools_cmd", operations.ErrInvalidInput)
	}
	if set["tool"] && !json.Valid([]byte(*input)) {
		return operations.Failure(*name, operations.ErrInvalidInput)
	}
	if err := config.LoadEnv(); err != nil {
		return operations.Failure("tools_cmd", operations.ErrConfiguration)
	}
	cfg, err := config.LoadTools()
	if err != nil {
		return operations.Failure("tools_cmd", operations.ErrConfiguration)
	}
	if *list {
		definitions, err := tools.Definitions(cfg)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(definitions)
	}
	selected := *name
	if *mcpTools {
		selected = tools.QueryLog
	}
	cfg, err = tools.Select(cfg, selected)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connections, err := client.OpenOperations(ctx, cfg)
	if err != nil {
		return err
	}
	defer connections.Close()
	if *mcpTools {
		return json.NewEncoder(os.Stdout).Encode(connections.MCP.Tools())
	}
	deps := tools.Dependencies{Prometheus: connections.Prometheus, Logs: connections.Logs}
	if cfg.QueryInternalDocsEnabled {
		embCfg, err := config.LoadEmbedding()
		if err != nil {
			return operations.Failure(selected, operations.ErrConfiguration)
		}
		dbCfg, err := config.LoadMilvus()
		if err != nil {
			return operations.Failure(selected, operations.ErrConfiguration)
		}
		emb, err := client.NewEmbedding(ctx, embCfg)
		if err != nil {
			return operations.Failure(selected, err)
		}
		defer emb.Close()
		db, err := client.NewMilvus(ctx, dbCfg)
		if err != nil {
			return operations.Failure(selected, err)
		}
		defer db.Close()
		store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
		if err != nil {
			return operations.Failure(selected, err)
		}
		retrievalCfg, err := config.LoadRetrieval()
		if err != nil {
			return operations.Failure(selected, operations.ErrConfiguration)
		}
		deps.Retriever, err = retriever.NewMilvusRetriever(store, retrievalCfg)
		if err != nil {
			return operations.Failure(selected, err)
		}
	}
	registry, err := tools.NewRegistry(cfg, deps)
	if err != nil {
		return err
	}
	output, err := registry.Invoke(ctx, selected, *input)
	if err != nil {
		return err
	}
	if !json.Valid([]byte(output)) {
		return fmt.Errorf("invalid tool output")
	}
	return json.NewEncoder(os.Stdout).Encode(json.RawMessage(output))
}
