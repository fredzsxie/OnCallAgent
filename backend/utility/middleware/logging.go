package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gogf/gf/v2/net/ghttp"
)

type observationKey struct{}
type requestLog struct {
	mu      sync.Mutex
	started time.Time
	err     error
}

// ObserveHTTP wraps the entire request lifecycle, including unmatched routes and SSE.
func ObserveHTTP(s *ghttp.Server, root context.Context) {
	s.SetAccessLogEnabled(false)
	s.SetErrorLogEnabled(false)
	s.SetDumpRouterMap(false)
	s.SetLogStdout(false)
	s.Logger().SetStdoutPrint(false)
	s.BindHookHandler("/*", ghttp.HookBeforeServe, func(r *ghttp.Request) {
		ctx, id := logger.WithRequest(logger.Inherit(r.Context(), root))
		r.SetCtx(context.WithValue(ctx, observationKey{}, &requestLog{started: time.Now()}))
		r.Response.Header().Set("X-Request-ID", id)
		log := logger.Info
		if r.URL.Path == "/api/health" {
			log = logger.Debug
		}
		log(r.Context(), "HTTP", "请求开始", logger.F("method", r.Method), logger.F("route", route(r)))
	})
	s.BindHookHandler("/*", ghttp.HookAfterOutput, func(r *ghttp.Request) {
		obs, _ := r.Context().Value(observationKey{}).(*requestLog)
		if obs == nil {
			return
		}
		obs.mu.Lock()
		err := obs.err
		obs.mu.Unlock()
		if err == nil {
			err = r.GetError()
		}
		fields := []logger.Field{logger.F("method", r.Method), logger.F("route", route(r)), logger.F("status", r.Response.Status), logger.F("elapsed", time.Since(obs.started))}
		code, outcome := "", "success"
		if err != nil {
			code, outcome = responseCode(err), "failed"
		}
		if errors.Is(err, context.Canceled) || r.Context().Err() == context.Canceled {
			code, outcome = "cancelled", "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code, outcome = "timeout", "timeout"
		}
		if r.Response.Status >= 400 && code == "" {
			code, outcome = "http_error", "failed"
		}
		fields = append(fields, logger.F("outcome", outcome), logger.F("code", code))
		log, message := logger.Info, "请求完成"
		if outcome != "success" {
			message = "请求未完成"
			log = logger.Error
			if outcome == "cancelled" || (r.Response.Status >= 400 && r.Response.Status < 500) {
				log = logger.Warn
			}
		} else if r.URL.Path == "/api/health" {
			log = logger.Debug
		}
		log(r.Context(), "HTTP", message, fields...)
	})
}

func SetExecutionError(r *ghttp.Request, err error) {
	if obs, ok := r.Context().Value(observationKey{}).(*requestLog); ok {
		obs.mu.Lock()
		obs.err = err
		obs.mu.Unlock()
	}
}

func route(r *ghttp.Request) string {
	// Log route templates rather than attacker-controlled URLs, queries or path values.
	for _, item := range r.Server.GetRoutes() {
		if item.IsServiceHandler && item.Route == r.URL.Path {
			return item.Route
		}
	}
	if r.Router != nil && r.Router.Uri != "/*" {
		return r.Router.Uri
	}
	return "<unmatched>"
}

func responseCode(err error) string {
	if code := logger.Code(err); code != "execution_failed" {
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
