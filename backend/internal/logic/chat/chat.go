package chat

import (
	"context"
	"errors"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

var ErrInvalidInput = errors.New("Id and Question must not be empty")

type ChatService struct {
	model client.ModelClient
}

func NewChatService(model client.ModelClient) *ChatService {
	return &ChatService{model: model}
}

// Chat is a single turn. Id is retained for the later conversation-memory stage.
func (s *ChatService) Chat(ctx context.Context, id, question string) (string, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(question) == "" {
		return "", ErrInvalidInput
	}
	return s.model.Generate(ctx, question)
}
