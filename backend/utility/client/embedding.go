package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/embedding/ark"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type Embedding struct {
	model *ark.Embedder
	cfg   config.Embedding
	http  *http.Client
	ctx   context.Context
}

var _ embedding.Embedder = (*Embedding)(nil)

func NewEmbedding(ctx context.Context, cfg config.Embedding) (*Embedding, error) {
	h := &http.Client{
		Transport: &observedTransport{http.DefaultTransport.(*http.Transport).Clone(), "Embed", "embedding"}, Timeout: cfg.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	// Vision models use the multimodal API even when the input contains only text.
	apiType, retries, concurrent := ark.APITypeMultiModal, 0, 1
	model, err := ark.NewEmbedder(ctx, &ark.EmbeddingConfig{
		BaseURL: strings.TrimRight(cfg.BaseURL, "/"), Model: cfg.Model, APIKey: cfg.APIKey, HTTPClient: h,
		APIType: &apiType, RetryTimes: &retries, MaxConcurrentRequests: &concurrent,
	})
	if err != nil {
		h.CloseIdleConnections()
		return nil, errors.New("cannot initialize embedding client")
	}
	logger.Info(ctx, "Embed", "Embedding 客户端已就绪")
	return &Embedding{model: model, cfg: cfg, http: h, ctx: ctx}, nil
}

func (c *Embedding) Close() {
	c.http.CloseIdleConnections()
	logger.Debug(c.ctx, "Embed", "Embedding 客户端已关闭")
}

func (c *Embedding) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) (out [][]float64, err error) {
	started := time.Now()
	defer func() {
		dim := 0
		if len(out) > 0 {
			dim = len(out[0])
		}
		logger.Finish(ctx, "Embed", "向量生成", started, err, logger.F("count", len(texts)), logger.F("dimension", dim))
	}()
	if len(texts) == 0 {
		return nil, errors.New("embedding input must not be empty")
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("embedding text must not be empty")
		}
	}
	options := embedding.GetCommonOptions(nil, opts...)
	if options.Model != nil && *options.Model != c.cfg.Model {
		return nil, errors.New("embedding model override would mix incompatible vector spaces")
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	vectors, err := c.model.EmbedStrings(ctx, texts)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Do not expose provider errors, which may contain input or credentials.
		return nil, errors.New("embedding request failed")
	}
	if len(vectors) != len(texts) {
		return nil, errors.New("embedding response count does not match input")
	}
	dim := len(vectors[0])
	for _, vector := range vectors {
		if _, err := FloatVector(vector, dim); err != nil {
			return nil, err
		}
	}
	return vectors, nil
}

func FloatVector(vector []float64, dimension int) ([]float32, error) {
	if dimension < 1 || dimension > 32768 || len(vector) != dimension {
		return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", dimension, len(vector))
	}
	out := make([]float32, dimension)
	nonzero := false
	for i, v := range vector {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > math.MaxFloat32 {
			return nil, errors.New("embedding contains a non-finite or out-of-range float32 value")
		}
		out[i] = float32(v)
		nonzero = nonzero || out[i] != 0
	}
	if !nonzero {
		return nil, errors.New("COSINE requires a nonzero embedding vector")
	}
	return out, nil
}
