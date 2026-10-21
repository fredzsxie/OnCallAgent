package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

var ErrModelRequest = errors.New("model request failed")

type ModelClient interface {
	Generate(context.Context, string) (string, error)
}

type OpenAI struct {
	model *openai.ChatModel
	http  *http.Client
	cfg   config.ChatModel
	ctx   context.Context
	role  string
}

func NewOpenAI(ctx context.Context, cfg config.ChatModel, roles ...string) (*OpenAI, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	role := "chat"
	if len(roles) > 0 && (roles[0] == "planner" || roles[0] == "executor") {
		role = roles[0]
	}
	started := time.Now()
	logger.Info(ctx, "Model", "初始化模型客户端", logger.F("role", role))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{
		Transport: &observedTransport{transport, "Model", role},
		Timeout:   cfg.Timeout,
		// Do not forward credentials to a redirected API address.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	model, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: strings.TrimRight(cfg.BaseURL, "/"),
		Model:   cfg.Model, APIKey: cfg.APIKey, HTTPClient: httpClient,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.New("could not initialize Eino ChatModel")
	}
	logger.Info(ctx, "Model", "模型客户端已就绪", logger.F("role", role), logger.F("elapsed", time.Since(started)))
	return &OpenAI{model: model, http: httpClient, cfg: cfg, ctx: ctx, role: role}, nil
}

func (c *OpenAI) Generate(ctx context.Context, question string) (answer string, err error) {
	started := time.Now()
	defer func() { logger.Finish(ctx, "Model", "模型生成", started, err, logger.F("role", c.role)) }()
	// Preserve request cancellation and any earlier deadline from the parent context.
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	out, err := c.model.Generate(ctx, []*schema.Message{schema.UserMessage(question)})
	if err != nil {
		// Keep cancellation and timeout distinguishable before masking provider errors.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var networkErr net.Error
		if errors.As(err, &networkErr) && networkErr.Timeout() {
			return "", context.DeadlineExceeded
		}
		// Provider errors may contain credentials or submitted text; expose only a safe sentinel.
		return "", ErrModelRequest
	}
	if out == nil || strings.TrimSpace(out.Content) == "" {
		return "", ErrModelRequest
	}
	return out.Content, nil
}

func (c *OpenAI) Close() {
	c.http.CloseIdleConnections()
	logger.Debug(c.ctx, "Model", "模型客户端已关闭", logger.F("role", c.role))
}

// ToolCallingModel shares the configured HTTP client without changing ordinary chat behavior.
func (c *OpenAI) ToolCallingModel() model.ToolCallingChatModel { return observedModel{c.model, c.role} }
