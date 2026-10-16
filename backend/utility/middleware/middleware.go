package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
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
	data := r.GetHandlerResponse()
	if err := r.GetError(); err != nil {
		// Map failure categories to public messages without echoing upstream error details.
		status := http.StatusInternalServerError
		message = "internal server error"
		data = nil
		switch {
		case errors.Is(err, context.Canceled):
			status, message = http.StatusRequestTimeout, "request cancelled"
		case errors.Is(err, knowledge.ErrSave):
			status, message = http.StatusInternalServerError, "file save failed"
			data = map[string]any{"stage": "save", "retryable": true}
		case errors.Is(err, knowledge.ErrIngest) && errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusGatewayTimeout, "knowledge ingestion timed out"
			data = map[string]any{"stage": "ingest", "retryable": true}
		case errors.Is(err, knowledge.ErrIngest):
			status, message = http.StatusServiceUnavailable, "knowledge ingestion failed; retry the upload"
			data = map[string]any{"stage": "ingest", "retryable": true}
		case errors.Is(err, knowledge.ErrTooLarge):
			status, message = http.StatusRequestEntityTooLarge, "file is too large"
		case errors.Is(err, knowledge.ErrUnsupportedFormat):
			status, message = http.StatusUnsupportedMediaType, "unsupported file format"
		case errors.Is(err, knowledge.ErrInvalidDocument):
			status, message = http.StatusBadRequest, "invalid or empty document"
		case errors.Is(err, knowledge.ErrRetryable) && errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusGatewayTimeout, "knowledge ingestion timed out"
			data = map[string]bool{"retryable": true}
		case errors.Is(err, knowledge.ErrRetryable) && !errors.Is(err, context.Canceled):
			status, message = http.StatusServiceUnavailable, "knowledge ingestion failed; retry the upload"
			data = map[string]bool{"retryable": true}

		case errors.Is(err, chat.ErrInvalidInput), gerror.Code(err) == gcode.CodeValidationFailed, gerror.Code(err) == gcode.CodeInvalidParameter:
			status, message = http.StatusBadRequest, "invalid request"
		case errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusGatewayTimeout, "model request timed out"
		case errors.Is(err, client.ErrModelRequest):
			status, message = http.StatusBadGateway, "model request failed"
		}
		r.Response.WriteHeader(status)
	}
	r.Response.WriteJson(Response{Message: message, Data: data})
}
