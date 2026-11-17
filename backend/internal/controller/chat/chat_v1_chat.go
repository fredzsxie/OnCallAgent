package chat

import (
	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

func (c *ControllerV1) Chat(request *gin.Context) {
	var req v1.ChatReq
	if request.ShouldBindJSON(&req) != nil {
		middleware.Respond(request, nil, middleware.ErrInvalidRequest)
		return
	}
	ctx := request.Request.Context()
	logger.Info(ctx, "Chat", "用户请求", logger.F("Id", req.Id), logger.F("Question", req.Question))
	answer, err := c.service.Chat(ctx, req.Id, req.Question)
	middleware.Respond(request, &v1.ChatRes{Answer: answer}, err)
}
