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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino-ext/devops"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	recall "github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("chat_cmd", flag.ContinueOnError)
	query := flags.String("query", "", "question for the chat agent")
	id := flags.String("id", "", "caller-supplied conversation label; no persisted history")
	history := flags.String("history", "[]", "JSON array of user/assistant messages")
	input := flags.String("input", "", "JSON UserMessage, exclusive with --query/--id/--history")
	stream := flags.Bool("stream", false, "write final-answer message chunks as NDJSON")
	dev := flags.Bool("dev", false, "register with Eino Dev and wait for signals")
	port := flags.Int("dev-port", 52538, "Eino Dev server port")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *port < 1 || *port > 65535 {
		return errors.New("--dev-port must be between 1 and 65535")
	}
	if set["input"] && (set["query"] || set["id"] || set["history"]) {
		return errors.New("--input cannot be combined with --query, --id or --history")
	}
	message := &pipeline.UserMessage{ID: *id, Query: *query}
	hasInput := set["input"] || set["query"]
	if set["input"] {
		if err := json.Unmarshal([]byte(*input), &message); err != nil {
			return errors.New("--input must be a JSON UserMessage")
		}
	} else if err := json.Unmarshal([]byte(*history), &message.History); err != nil {
		return errors.New("--history must be a JSON message array")
	}
	if !hasInput && !*dev {
		return errors.New("--query or --input is required unless --dev is enabled")
	}
	if !hasInput && (set["id"] || set["history"] || *stream) {
		return errors.New("--id, --history and --stream require an input")
	}
	if hasInput {
		if message == nil || strings.TrimSpace(message.Query) == "" {
			return pipeline.ErrInvalidMessage
		}
		for _, item := range message.History {
			if item == nil || (item.Role != "user" && item.Role != "assistant") || len(item.ToolCalls) != 0 {
				return pipeline.ErrInvalidMessage
			}
		}
	}
	output := os.Stdout
	if *dev {
		// DevOps logs use os.Stdout; reserve the original output for machine-readable results.
		os.Stdout = os.Stderr
	}
	if err := config.LoadEnv(); err != nil {
		return err
	}
	agentCfg, err := config.LoadChatAgent()
	if err != nil {
		return err
	}
	toolsCfg, err := config.LoadTools()
	if err != nil {
		return err
	}
	retrievalCfg, err := config.LoadRetrieval()
	if err != nil {
		return err
	}
	modelCfg, err := config.LoadChatModel()
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
	modelClient, err := client.NewOpenAI(ctx, modelCfg)
	if err != nil {
		return err
	}
	defer modelClient.Close()
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
	retriever, err := recall.NewMilvusRetriever(store, retrievalCfg)
	if err != nil {
		return err
	}
	registry, err := tools.NewRegistry(toolsCfg, retriever, time.Now)
	if err != nil {
		return err
	}
	// DevOps installs compile callbacks and must run before either graph is compiled.
	if *dev {
		if err := devops.Init(ctx, devops.WithDevServerPort(strconv.Itoa(*port))); err != nil {
			return err
		}
	}
	graph, err := pipeline.BuildChatAgent(ctx, pipeline.Config{Agent: agentCfg, TimeZone: toolsCfg.TimeZone}, pipeline.Dependencies{
		Model: modelClient.ToolCallingModel(), Retriever: retriever, Tools: registry.Tools(),
	})
	if err != nil {
		return err
	}
	if hasInput {
		encoder := json.NewEncoder(output)
		if *stream {
			reader, err := graph.Stream(ctx, message)
			if err != nil {
				return err
			}
			defer reader.Close()
			for {
				chunk, err := reader.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				if err = encoder.Encode(chunk); err != nil {
					return err
				}
			}
		} else {
			answer, err := graph.Invoke(ctx, message)
			if err != nil {
				return err
			}
			if err = encoder.Encode(answer); err != nil {
				return err
			}
		}
	}
	if *dev {
		fmt.Fprintf(os.Stderr, "ChatAgent registered on port %d; waiting for SIGINT/SIGTERM\n", *port)
		<-ctx.Done()
	}
	return nil
}
