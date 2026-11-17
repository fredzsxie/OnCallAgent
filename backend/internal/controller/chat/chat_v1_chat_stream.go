package chat

import (
	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

func (c *ControllerV1) ChatStream(request *gin.Context) {
	var req v1.ChatStreamReq
	if request.ShouldBindJSON(&req) != nil {
		middleware.Respond(request, nil, middleware.ErrInvalidRequest)
		return
	}
	ctx := request.Request.Context()
	err := c.streams.Serve(ctx, request.Writer, req.Id, req.Question)
	middleware.Respond(request, nil, err)
}
