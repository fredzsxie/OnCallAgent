package chat

import (
	"context"
	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	"github.com/gogf/gf/v2/frame/g"
)

func (c *ControllerV1) ChatStream(ctx context.Context, req *v1.ChatStreamReq) (*v1.ChatStreamRes, error) {
	return nil, c.streams.Serve(ctx, g.RequestFromCtx(ctx), req.Id, req.Question)
}
