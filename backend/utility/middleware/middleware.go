package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/fredzsxie/OnCallAgent/backend/internal/logic/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/net/ghttp"
)

type Response struct {
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func ResponseMiddleware(r *ghttp.Request) {
	r.Middleware.Next()
	message := "OK"
	if err := r.GetError(); err != nil {
		status := http.StatusInternalServerError
		message = "internal server error"
		switch {
		case errors.Is(err, chat.ErrInvalidInput), gerror.Code(err) == gcode.CodeValidationFailed, gerror.Code(err) == gcode.CodeInvalidParameter:
			status, message = http.StatusBadRequest, "invalid request"
		case errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusGatewayTimeout, "model request timed out"
		case errors.Is(err, context.Canceled):
			status, message = http.StatusRequestTimeout, "request cancelled"
		case errors.Is(err, client.ErrModelRequest):
			status, message = http.StatusBadGateway, "model request failed"
		}
		r.Response.WriteHeader(status)
	}
	r.Response.WriteJson(Response{Message: message, Data: r.GetHandlerResponse()})
}
