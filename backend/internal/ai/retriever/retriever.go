package retriever

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

var (
	ErrInvalidQuery = errors.New("query must not be empty")
	ErrQueryFailed  = errors.New("knowledge retrieval failed")
)

type MilvusRetriever struct {
	base eino.Retriever
	cfg  config.Retrieval
}

var _ eino.Retriever = (*MilvusRetriever)(nil)

type Document struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Source  string  `json:"source"`
	Title   string  `json:"title"`
	Score   float64 `json:"score"`
}

type Result struct {
	Query     string     `json:"query"`
	Documents []Document `json:"documents"`
}

func NewMilvusRetriever(base eino.Retriever, cfg config.Retrieval) (*MilvusRetriever, error) {
	if base == nil {
		return nil, errors.New("retriever backend is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &MilvusRetriever{base: base, cfg: cfg}, nil
}

func (r *MilvusRetriever) Retrieve(ctx context.Context, query string, opts ...eino.Option) ([]*schema.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, queryError(err)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrInvalidQuery
	}
	options := eino.GetCommonOptions(nil, opts...)
	if options.Index != nil || options.SubIndex != nil || options.Embedding != nil || len(options.DSLInfo) != 0 {
		return nil, errors.New("index, sub-index, embedder and DSL overrides are unsupported")
	}
	cfg := r.cfg
	if options.TopK != nil {
		cfg.TopK = *options.TopK
	}
	if options.ScoreThreshold != nil {
		cfg.ScoreThreshold = *options.ScoreThreshold
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	docs, err := r.base.Retrieve(ctx, query, eino.WithTopK(cfg.TopK), eino.WithScoreThreshold(cfg.ScoreThreshold))
	if err != nil {
		return nil, queryError(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, queryError(err)
	}
	result := make([]*schema.Document, 0, len(docs))
	for _, doc := range docs {
		if doc == nil || math.IsNaN(doc.Score()) || math.IsInf(doc.Score(), 0) {
			return nil, ErrQueryFailed
		}
		if doc.Score() >= cfg.ScoreThreshold {
			result = append(result, doc)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Score() > result[j].Score() })
	return result[:min(len(result), cfg.TopK)], nil
}

func (r *MilvusRetriever) Search(ctx context.Context, query string) (Result, error) {
	docs, err := r.Retrieve(ctx, query)
	if err != nil {
		return Result{}, err
	}
	result := Result{Query: strings.TrimSpace(query), Documents: make([]Document, 0, len(docs))}
	for _, doc := range docs {
		source, _ := doc.MetaData["source"].(string)
		title, _ := doc.MetaData["title"].(string)
		result.Documents = append(result.Documents, Document{doc.ID, doc.Content, source, title, doc.Score()})
	}
	return result, nil
}

// Preserve cancellation semantics without exposing provider errors or credentials.
func queryError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.Join(ErrQueryFailed, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrQueryFailed, context.DeadlineExceeded)
	}
	return ErrQueryFailed
}
