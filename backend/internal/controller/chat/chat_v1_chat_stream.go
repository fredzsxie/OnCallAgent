package chat

import (
	"context"
	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gogf/gf/v2/frame/g"
)

func (c *ControllerV1) ChatStream(ctx context.Context, req *v1.ChatStreamReq) (*v1.ChatStreamRes, error) {
	logger.Info(ctx, "Chat", "用户请求", logger.F("Id", req.Id), logger.F("Question", req.Question))
	return nil, c.streams.Serve(ctx, g.RequestFromCtx(ctx), req.Id, req.Question)
}
