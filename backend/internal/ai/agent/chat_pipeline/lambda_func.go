package chat_pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

func validateInput(ctx context.Context, input *UserMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if input == nil || strings.TrimSpace(input.Query) == "" {
		return ErrInvalidMessage
	}
	for _, msg := range input.History {
		if msg == nil || (msg.Role != schema.User && msg.Role != schema.Assistant) || len(msg.ToolCalls) != 0 {
			return ErrInvalidMessage
		}
	}
	return nil
}

func newInputToRagLambda(ctx context.Context, input *UserMessage) (string, error) {
	if err := validateInput(ctx, input); err != nil {
		return "", err
	}
	return strings.TrimSpace(input.Query), nil
}

func newInputToChatLambda(location *time.Location, now func() time.Time) func(context.Context, *UserMessage) (map[string]any, error) {
	return func(ctx context.Context, input *UserMessage) (map[string]any, error) {
		if err := validateInput(ctx, input); err != nil {
			return nil, err
		}
		history := make([]*schema.Message, 0, len(input.History))
		for _, msg := range input.History {
			history = append(history, &schema.Message{Role: msg.Role, Content: msg.Content})
		}
		return map[string]any{"content": strings.TrimSpace(input.Query), "history": history, "date": now().In(location).Format(time.RFC3339Nano)}, nil
	}
}
