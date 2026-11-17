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
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gin-gonic/gin"
)

var ErrInvalidRequest = errors.New("invalid request")

type Response struct {
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// Respond preserves the JSON envelope and never appends JSON to a started stream.
func Respond(c *gin.Context, data any, err error) {
	if err != nil {
		SetExecutionError(c.Request.Context(), err)
	}
	if c.Writer.Written() {
		return
	}
	if err != nil {
		status, body := ErrorResponse(err)
		c.JSON(status, body)
		return
	}
	c.JSON(http.StatusOK, Response{Message: "OK", Data: data})
}

// Recovery observes panics without exposing panic values or request credentials.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err := errors.New("request handler panicked")
				if c.Request.URL.Path == "/api/ai_ops" || c.Request.URL.Path == "/api/ai_ops_stream" {
					err = aiops.Wrap(aiops.ErrInternal, nil)
				}
				logger.Error(c.Request.Context(), "HTTP", "请求处理发生异常", logger.F("error_type", "panic"))
				Respond(c, nil, err)
				c.Abort()
			}
		}()
		c.Next()
	}
}

// Keep public error messages identical before and after the SSE response starts.
func ErrorResponse(err error) (int, Response) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		err = knowledge.ErrTooLarge
	}
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
	case errors.Is(err, knowledge.ErrNotFound):
		status, message = http.StatusNotFound, "knowledge document not found"
	case errors.Is(err, knowledge.ErrConflict):
		status, message = http.StatusConflict, "knowledge source or version conflict"
	case errors.Is(err, knowledge.ErrInvalidMetadata), errors.Is(err, knowledge.ErrInvalidSearch):
		status, message = http.StatusBadRequest, "invalid knowledge parameters"
	case errors.Is(err, knowledge.ErrSearchFailed):
		status, message = http.StatusBadGateway, "knowledge search failed"
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

	case errors.Is(err, chat.ErrInvalidInput), errors.Is(err, ErrInvalidRequest):
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
