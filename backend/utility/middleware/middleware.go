package middleware

import (
	"net/http"

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
		if gerror.Code(err) == gcode.CodeValidationFailed || gerror.Code(err) == gcode.CodeInvalidParameter {
			status, message = http.StatusBadRequest, "invalid request"
		}
		r.Response.WriteHeader(status)
	}
	r.Response.WriteJson(Response{Message: message, Data: r.GetHandlerResponse()})
}
