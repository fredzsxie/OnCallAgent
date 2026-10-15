package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

var ErrModelRequest = errors.New("model request failed")

// ModelClient is the small interface ChatService needs; Eino stays in this adapter.
type ModelClient interface {
	Generate(context.Context, string) (string, error)
}

type OpenAI struct {
	model *openai.ChatModel
	http  *http.Client
	cfg   config.ChatModel
}

// NewOpenAI creates one reusable Eino model and its HTTP connection pool.
func NewOpenAI(ctx context.Context, cfg config.ChatModel) (*OpenAI, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		// An API redirect is a configuration issue, not a reason to forward a key.
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
	return &OpenAI{model: model, http: httpClient, cfg: cfg}, nil
}

func (c *OpenAI) Generate(ctx context.Context, question string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	out, err := c.model.Generate(ctx, []*schema.Message{schema.UserMessage(question)})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var networkErr net.Error
		if errors.As(err, &networkErr) && networkErr.Timeout() {
			return "", context.DeadlineExceeded
		}
		// Provider errors can contain credentials or submitted text; do not expose them.
		return "", ErrModelRequest
	}
	if out == nil || strings.TrimSpace(out.Content) == "" {
		return "", ErrModelRequest
	}
	return out.Content, nil
}

func (c *OpenAI) Close() {
	c.http.CloseIdleConnections()
}
