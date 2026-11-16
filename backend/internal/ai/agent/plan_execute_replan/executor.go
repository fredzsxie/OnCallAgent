package plan_execute_replan

import (
	"context"
	"encoding/json"
	"errors"

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
			var current plan
			data, err := in.Plan.MarshalJSON()
			if err != nil {
				return nil, err
			}
			if err = current.UnmarshalJSON(data); err != nil {
				return nil, err
			}
			beginExecution(ctx, current.Steps)
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
		id := startTool(ctx, in.Name, toolInput(in.Name, in.Arguments))
		output, err := next(ctx, in)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		var detail *StepOutput
		var count *int
		var summary string
		if err == nil {
			if output == nil {
				err = operations.ErrInvalidResponse
			} else {
				detail, count, summary, err = toolResult(in.Name, output.Result)
			}
		}
		if err != nil {
			if errors.Is(err, retriever.ErrInvalidQuery) {
				err = operations.ErrInvalidInput
			}
			failure := operations.Failure(in.Name, err)
			public := publicToolError(failure)
			status := "failed"
			if errors.Is(err, context.Canceled) {
				status = "cancelled"
			}
			endTool(ctx, id, status, public.Message, nil, public, nil)
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil, err
			}
			data, _ := json.Marshal(map[string]any{"error": failure})
			return &compose.ToolOutput{Result: string(data)}, nil
		}
		endTool(ctx, id, "completed", summary, detail, nil, count)
		return output, nil
	}
}
