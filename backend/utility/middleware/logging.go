package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gin-gonic/gin"
)

type observationKey struct{}
type requestLog struct {
	mu  sync.Mutex
	err error
}

// responseWriter retains Gin's accounting while keeping the original transport controls.
type responseWriter struct {
	gin.ResponseWriter
	transport http.ResponseWriter
}

func (w *responseWriter) Unwrap() http.ResponseWriter          { return w.ResponseWriter }
func (w *responseWriter) TransportWriter() http.ResponseWriter { return w.transport }

// ObserveHTTP includes unmatched routes and records one terminal result after recovery.
func ObserveHTTP(root context.Context) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		ctx, id := logger.WithRequest(logger.Inherit(c.Request.Context(), root), c.GetHeader("X-Request-ID"))
		obs := &requestLog{}
		c.Request = c.Request.WithContext(context.WithValue(ctx, observationKey{}, obs))
		c.Request.Header.Set("X-Request-ID", id)
		c.Header("X-Request-ID", id)
		// Only unwrap the framework layer; custom transport wrappers own their error semantics.
		base := c.Writer.(interface{ Unwrap() http.ResponseWriter }).Unwrap()
		c.Writer = &responseWriter{ResponseWriter: c.Writer, transport: base}
		route := c.FullPath()
		if route == "" {
			route = "<unmatched>"
		}
		log := logger.Info
		if c.Request.URL.Path == "/api/health" {
			log = logger.Debug
		}
		log(ctx, "HTTP", "请求开始", logger.F("method", c.Request.Method), logger.F("route", route))
		defer func() {
			obs.mu.Lock()
			err := obs.err
			obs.mu.Unlock()
			status := c.Writer.Status()
			errorType, outcome := "", "success"
			if err != nil {
				errorType, outcome = responseErrorType(err), "failed"
			}
			if errors.Is(err, context.Canceled) || c.Request.Context().Err() == context.Canceled {
				errorType, outcome = "cancelled", "cancelled"
			} else if errors.Is(err, context.DeadlineExceeded) || c.Request.Context().Err() == context.DeadlineExceeded {
				errorType, outcome = "timeout", "timeout"
			}
			if status >= 400 && errorType == "" {
				errorType, outcome = "http_error", "failed"
			}
			fields := []logger.Field{logger.F("method", c.Request.Method), logger.F("route", route), logger.F("code", status), logger.F("elapsed", time.Since(started)), logger.F("outcome", outcome), logger.F("error_type", errorType)}
			message := "请求完成"
			if outcome != "success" {
				message, log = "请求未完成", logger.Error
				if outcome == "cancelled" || (status >= 400 && status < 500) {
					log = logger.Warn
				}
			}
			log(ctx, "HTTP", message, fields...)
		}()
		c.Next()
	}
}

// SetExecutionError records business and transport failures even after SSE sends HTTP 200.
func SetExecutionError(ctx context.Context, err error) {
	if obs, ok := ctx.Value(observationKey{}).(*requestLog); ok && err != nil {
		obs.mu.Lock()
		obs.err = err
		obs.mu.Unlock()
	}
}

func responseErrorType(err error) string {
	if code := logger.ErrorType(err); code != "execution_failed" {
		return code
	}
	status, body := ErrorResponse(err)
	if data, ok := body.Data.(map[string]any); ok {
		if code, ok := data["code"].(string); ok {
			return code
		}
		if stage, ok := data["stage"].(string); ok {
			return stage + "_failed"
		}
	}
	switch status {
	case http.StatusBadRequest:
		return "invalid_input"
	case http.StatusUnsupportedMediaType:
		return "unsupported_format"
	case http.StatusRequestEntityTooLarge:
		return "file_too_large"
	case http.StatusBadGateway:
		return "execution_failed"
	default:
		return "internal_error"
	}
}
