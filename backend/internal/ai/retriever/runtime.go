package retriever

import (
	"context"
	"errors"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

// NewConfigured gives every CLI the same catalog validation and score semantics as HTTP.
func NewConfigured(ctx context.Context, store *knowledge.Store, cfg config.Retrieval, model string) (*MilvusRetriever, func() error, error) {
	close := func() error { return nil }
	if !store.HybridEnabled() {
		if cfg.Mode != "" && cfg.Mode != knowledge.Dense {
			return nil, close, errors.New("hybrid retrieval requires a v2 collection; run knowledge_migrate")
		}
		r, err := NewMilvusRetriever(store, cfg)
		return r, close, err
	}
	kc, err := config.LoadKnowledge()
	if err != nil {
		return nil, close, err
	}
	c, err := knowledge.OpenCatalog(kc.CatalogPath)
	if err != nil {
		return nil, close, err
	}
	if err = c.Bind(ctx, store.Profile(model)); err != nil {
		c.Close()
		return nil, close, err
	}
	mode := cfg.Mode
	if mode == "" {
		mode = knowledge.Dense
	}
	s, err := knowledge.NewSearchService(c, store, knowledge.SearchConfig{Mode: mode, TopK: cfg.TopK, DenseMinScore: cfg.ScoreThreshold, Timeout: store.Timeout()})
	if err != nil {
		c.Close()
		return nil, close, err
	}
	r, err := NewMilvusRetriever(s, cfg)
	if err != nil {
		c.Close()
		return nil, close, err
	}
	return r, c.Close, nil
}
