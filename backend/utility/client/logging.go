package client

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type observedModel struct {
	model.ToolCallingChatModel
	role string
}

func (m observedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return observedModel{bound, m.role}, nil
}

func (m observedModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (out *schema.Message, err error) {
	started := time.Now()
	defer func() { logger.Finish(ctx, "Model", "模型生成", started, err, logger.F("role", m.role)) }()
	return m.ToolCallingChatModel.Generate(ctx, messages, opts...)
}

type observedTransport struct {
	base            http.RoundTripper
	component, role string
}

func (t *observedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	started := time.Now()
	logger.Debug(r.Context(), t.component, "发起上游请求", logger.F("role", t.role), logger.F("method", r.Method))
	response, err := t.base.RoundTrip(r)
	if err != nil {
		if r.Context().Err() != nil {
			err = r.Context().Err()
		}
		logger.Failure(r.Context(), t.component, "上游连接失败", err, logger.F("role", t.role), logger.F("elapsed", time.Since(started)))
		return response, err
	}
	log := logger.Info
	if response.StatusCode >= 400 {
		log = logger.Warn
	}
	code := ""
	switch {
	case response.StatusCode == 401 || response.StatusCode == 403:
		code = "authentication_failed"
	case response.StatusCode == 429:
		code = "rate_limited"
	case response.StatusCode >= 500:
		code = "upstream_error"
	case response.StatusCode >= 400:
		code = "invalid_request"
	case response.StatusCode >= 300:
		code = "redirect_rejected"
		log = logger.Warn
	}
	log(r.Context(), t.component, "收到上游响应头", logger.F("role", t.role), logger.F("status", response.StatusCode), logger.F("code", code), logger.F("headers_elapsed", time.Since(started)))
	response.Body = &observedBody{ReadCloser: response.Body, ctx: r.Context(), component: t.component, role: t.role, started: started, status: response.StatusCode}
	return response, nil
}

func (t *observedTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type observedBody struct {
	io.ReadCloser
	ctx             context.Context
	component, role string
	started         time.Time
	status          int
	once            sync.Once
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		if err == io.EOF {
			b.finish("eof", nil)
		} else {
			b.finish("failed", err)
		}
	}
	return n, err
}
func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish("closed", err)
	return err
}
func (b *observedBody) finish(outcome string, err error) {
	b.once.Do(func() {
		if b.ctx.Err() != nil {
			err = b.ctx.Err()
			outcome = logger.Code(err)
		}
		fields := []logger.Field{logger.F("role", b.role), logger.F("status", b.status), logger.F("outcome", outcome), logger.F("elapsed", time.Since(b.started))}
		if err != nil {
			logger.Failure(b.ctx, b.component, "上游响应读取失败", err, fields...)
			return
		}
		logger.Debug(b.ctx, b.component, "上游响应读取结束", fields...)
	})
}
