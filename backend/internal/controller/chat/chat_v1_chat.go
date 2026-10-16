package chat

import (
	"context"
	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
)

func (c *ControllerV1) Chat(ctx context.Context, req *v1.ChatReq) (*v1.ChatRes, error) {
	answer, err := c.service.Chat(ctx, req.Id, req.Question)
	if err != nil {
		return nil, err
	}
	return &v1.ChatRes{Answer: answer}, nil
}
