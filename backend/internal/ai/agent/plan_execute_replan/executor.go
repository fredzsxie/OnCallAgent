package plan_execute_replan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

func NewExecutor(ctx context.Context, m model.ToolCallingChatModel, tools []tool.BaseTool, maxSteps int) (adk.Agent, error) {
	return planexecute.NewExecutor(ctx, &planexecute.ExecutorConfig{Model: m, MaxIterations: maxSteps,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: toolMiddleware}}}},
		GenInputFn: func(ctx context.Context, in *planexecute.ExecutionContext) ([]adk.Message, error) {
			s := state(ctx)
			if s != nil {
				s.mu.Lock()
				s.iteration++
				s.mu.Unlock()
			}
			record(ctx, "Executor", "execute", "", "started", in.Plan.FirstStep())
			p, err := in.Plan.MarshalJSON()
			if err != nil {
				return nil, err
			}
			msgs, err := planexecute.ExecutorPrompt.Format(ctx, map[string]any{"input": in.UserInput, "plan": string(p), "executed_steps": in.ExecutedSteps, "step": in.Plan.FirstStep()})
			if err != nil {
				return nil, err
			}
			msgs[0].Content += "\n" + instructions
			return msgs, nil
		},
	})
}

func toolMiddleware(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
	return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
		output, err := next(ctx, in)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if err != nil {
			if errors.Is(err, retriever.ErrInvalidQuery) {
				err = operations.ErrInvalidInput
			}
			failure := operations.Failure(in.Name, err)
			record(ctx, "Executor", "tool", in.Name, "failed", failure.Code)
			data, _ := json.Marshal(map[string]any{"error": failure})
			return &compose.ToolOutput{Result: string(data)}, nil
		}
		if output == nil || !json.Valid([]byte(output.Result)) {
			return nil, ErrExecution
		}
		var result map[string]json.RawMessage
		_ = json.Unmarshal([]byte(output.Result), &result)
		summary := "tool completed"
		for _, key := range []string{"alerts", "documents", "records"} {
			if data, ok := result[key]; ok {
				var values []json.RawMessage
				_ = json.Unmarshal(data, &values)
				summary = fmt.Sprintf("%s=%d", key, len(values))
			}
		}
		record(ctx, "Executor", "tool", in.Name, "completed", summary)
		return output, nil
	}
}
