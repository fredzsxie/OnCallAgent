package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/embedding/ark"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type Embedding struct {
	model *ark.Embedder
	cfg   config.Embedding
	http  *http.Client
}

var _ embedding.Embedder = (*Embedding)(nil)

func NewEmbedding(ctx context.Context, cfg config.Embedding) (*Embedding, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	h := &http.Client{
		Transport: http.DefaultTransport.(*http.Transport).Clone(), Timeout: cfg.Timeout,
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
	return &Embedding{model: model, cfg: cfg, http: h}, nil
}

func (c *Embedding) Close() { c.http.CloseIdleConnections() }

func (c *Embedding) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	if len(texts) == 0 || len(texts) > 256 {
		return nil, errors.New("embedding input must contain between 1 and 256 texts")
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" || len(text) > 100000 {
			return nil, errors.New("embedding text must be nonempty and at most 100000 UTF-8 bytes")
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
		// Provider errors may echo credentials or input; expose only a status code.
		var apiErr *arkmodel.APIError
		if errors.As(err, &apiErr) {
			return nil, fmt.Errorf("embedding request failed (HTTP %d)", apiErr.HTTPStatusCode)
		}
		return nil, errors.New("embedding request failed")
	}
	if len(vectors) != len(texts) {
		return nil, errors.New("embedding response count does not match input")
	}
	dim := c.cfg.Dimensions
	if dim == 0 {
		dim = len(vectors[0])
	}
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
