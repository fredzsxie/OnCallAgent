package chat_pipeline

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type UserMessage struct {
	ID      string            `json:"id"`
	Query   string            `json:"query"`
	History []*schema.Message `json:"history"`
}

type Config struct {
	Agent    config.ChatAgent
	TimeZone string
}

type Dependencies struct {
	Model     model.ToolCallingChatModel
	Retriever retriever.Retriever
	Tools     []tool.BaseTool
	Now       func() time.Time
}

var (
	ErrInvalidMessage = errors.New("query and valid user/assistant history are required")
	ErrAgentFailed    = errors.New("chat agent execution failed")
)

type runState struct{ Deadline time.Time }

// Every invocation gets its own deadline; both branches and ReAct share that budget.
func executionContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	var deadline time.Time
	err := compose.ProcessState[*runState](ctx, func(_ context.Context, state *runState) error {
		deadline = state.Deadline
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	return bounded, cancel, nil
}

func safeError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	var networkErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return errors.Join(ErrAgentFailed, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()):
		return errors.Join(ErrAgentFailed, context.DeadlineExceeded)
	case errors.Is(err, compose.ErrExceedMaxSteps):
		return errors.Join(ErrAgentFailed, compose.ErrExceedMaxSteps)
	default:
		return ErrAgentFailed
	}
}
