package middleware

import (
	"context"
	"errors"
	"net/http"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	ops "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
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

type streamKey struct{}

func MarkStreaming(r *ghttp.Request) { r.SetCtxVar(streamKey{}, true) }

func ResponseMiddleware(r *ghttp.Request) {
	r.Middleware.Next()
	if r.GetCtxVar(streamKey{}).Bool() {
		return
	}
	if err := r.GetError(); err != nil {
		if r.URL.Path == "/api/ai_ops" {
			var failure *aiops.Error
			if !errors.As(err, &failure) {
				if gerror.Code(err) == gcode.CodeValidationFailed || gerror.Code(err) == gcode.CodeInvalidParameter {
					err = ops.ErrInvalidInput
				} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					err = aiops.ErrInternal
				}
				err = aiops.Wrap(err, nil)
			}
		}
		status, body := ErrorResponse(err)
		r.Response.WriteHeader(status)
		r.Response.WriteJson(body)
		return
	}
	r.Response.WriteJson(Response{Message: "OK", Data: r.GetHandlerResponse()})
}

// Keep public error messages identical before and after the SSE response starts.
func ErrorResponse(err error) (int, Response) {
	var failure *aiops.Error
	if errors.As(err, &failure) {
		status := http.StatusBadGateway
		switch {
		case errors.Is(failure, ops.ErrInvalidInput):
			status = http.StatusBadRequest
		case errors.Is(failure, context.Canceled):
			status = http.StatusRequestTimeout
		case errors.Is(failure, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
		case errors.Is(failure, aiops.ErrInitialization), errors.Is(failure, aiops.ErrInternal), errors.Is(failure, ops.ErrEventWrite):
			status = http.StatusInternalServerError
		}
		return status, Response{Message: failure.Message, Data: map[string]any{"code": failure.Code, "detail": failure.Detail}}
	}
	status := http.StatusInternalServerError
	message := "internal server error"
	var data any
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
	case errors.Is(err, pipeline.ErrAgentFailed):
		status, message = http.StatusBadGateway, "chat agent execution failed"
	case errors.Is(err, client.ErrModelRequest):
		status, message = http.StatusBadGateway, "model request failed"
	}
	return status, Response{Message: message, Data: data}
}
