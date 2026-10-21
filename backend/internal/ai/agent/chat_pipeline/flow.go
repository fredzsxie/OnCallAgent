package chat_pipeline

import (
	"context"
	"io"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func newReactAgentLambda(ctx context.Context, cfg Config, deps Dependencies) (*compose.Lambda, error) {
	model, err := newChatModel(deps)
	if err != nil {
		return nil, err
	}
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: model, MaxStep: cfg.Agent.MaxSteps,
		ToolsConfig:           compose.ToolsNodeConfig{Tools: deps.Tools},
		StreamToolCallChecker: streamHasToolCall,
	})
	if err != nil {
		return nil, ErrAgentFailed
	}
	invoke := func(ctx context.Context, input []*schema.Message, _ ...any) (out *schema.Message, err error) {
		started := time.Now()
		defer func() { logger.Finish(ctx, "Agent", "对话图执行", started, err, logger.F("mode", "invoke")) }()
		ctx, cancel, err := executionContext(ctx)
		if err != nil {
			return nil, err
		}
		defer cancel()
		output, err := agent.Generate(ctx, input)
		if err != nil {
			return nil, safeError(ctx, err)
		}
		if ctx.Err() != nil {
			return nil, safeError(ctx, ctx.Err())
		}
		return output, nil
	}
	stream := func(ctx context.Context, input []*schema.Message, _ ...any) (*schema.StreamReader[*schema.Message], error) {
		ctx, cancel, err := executionContext(ctx)
		if err != nil {
			return nil, err
		}
		started := time.Now()
		logger.Debug(ctx, "Agent", "开始流式图执行")
		upstream, err := agent.Stream(ctx, input)
		if err != nil {
			logger.Finish(ctx, "Agent", "流式图初始化", started, err)
			safe := safeError(ctx, err)
			cancel()
			return nil, safe
		}
		// Keep the deadline active until the stream finishes, rather than until Stream returns.
		output, writer := schema.Pipe[*schema.Message](0)
		go func() {
			var executionErr error
			defer func() {
				logger.Finish(ctx, "Agent", "流式图执行", started, executionErr, logger.F("mode", "stream"))
			}()
			defer cancel()
			defer upstream.Close()
			defer writer.Close()
			for {
				msg, err := upstream.Recv()
				if err == io.EOF {
					if ctx.Err() != nil {
						executionErr = ctx.Err()
						writer.Send(nil, safeError(ctx, ctx.Err()))
					}
					return
				}
				if err != nil {
					executionErr = err
					writer.Send(nil, safeError(ctx, err))
					return
				}
				if ctx.Err() != nil {
					executionErr = ctx.Err()
					writer.Send(nil, safeError(ctx, ctx.Err()))
					return
				}
				if writer.Send(msg, nil) {
					executionErr = context.Canceled
					return
				}
			}
		}()
		return output, nil
	}
	return compose.AnyLambda(invoke, stream, nil, nil)
}

// Inspect the whole model turn: gateways may emit text before tool-call deltas.
// Eino keeps a copy of the stream, so only the final-answer turn is replayed to callers.
// Read cancellation from the producer instead of abandoning a copied stream mid-send.
func streamHasToolCall(ctx context.Context, stream *schema.StreamReader[*schema.Message]) (bool, error) {
	defer stream.Close()
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return false, ctx.Err()
		}
		if err != nil {
			return false, err
		}
		if msg == nil {
			return false, ErrAgentFailed
		}
		if len(msg.ToolCalls) > 0 && ctx.Err() == nil {
			return true, nil
		}
	}
}
