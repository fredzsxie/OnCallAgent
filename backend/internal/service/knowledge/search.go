package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

var ErrInvalidSearch = errors.New("invalid knowledge search")
var ErrSearchFailed = errors.New("knowledge search failed")

const Dense = "dense"
const BM25 = "bm25"
const Hybrid = "hybrid"

func ValidMode(mode string) bool { return mode == Dense || mode == BM25 || mode == Hybrid }

type SearchRequest struct {
	Query         string   `json:"query"`
	Mode          string   `json:"mode"`
	TopK          int      `json:"topK"`
	Filters       Filters  `json:"filters"`
	DenseMinScore *float64 `json:"denseMinScore,omitempty"`
}
type SearchHit struct {
	ID          string  `json:"id"`
	ChunkIndex  int     `json:"chunkIndex"`
	DocumentID  string  `json:"documentId"`
	Version     string  `json:"version"`
	Title       string  `json:"title"`
	Source      string  `json:"source"`
	DocType     string  `json:"docType"`
	SectionPath string  `json:"sectionPath"`
	Content     string  `json:"content"`
	Score       float64 `json:"score"`
	ScoreType   string  `json:"scoreType"`
}
type SearchResult struct {
	Query      string      `json:"query"`
	Mode       string      `json:"mode"`
	ScoreType  string      `json:"scoreType"`
	Documents  []SearchHit `json:"documents"`
	DurationMs int64       `json:"durationMs"`
}
type SearchBackend interface {
	Candidates(context.Context, string, string, int, Filters) ([]*schema.Document, error)
}
type SearchConfig struct {
	Mode          string
	TopK          int
	DenseMinScore float64
	Timeout       time.Duration
}
type SearchService struct {
	catalog *Catalog
	backend SearchBackend
	cfg     SearchConfig
}

func NewSearchService(c *Catalog, b SearchBackend, cfg SearchConfig) (*SearchService, error) {
	if c == nil || b == nil || !ValidMode(cfg.Mode) || cfg.TopK < 1 || cfg.TopK > 16384 || cfg.Timeout <= 0 || math.IsNaN(cfg.DenseMinScore) || math.IsInf(cfg.DenseMinScore, 0) || cfg.DenseMinScore < -1 || cfg.DenseMinScore > 1 {
		return nil, ErrInvalidSearch
	}
	return &SearchService{catalog: c, backend: b, cfg: cfg}, nil
}
func (s *SearchService) Mode() string { return s.cfg.Mode }
func (s *SearchService) Search(ctx context.Context, r SearchRequest) (result SearchResult, err error) {
	started := time.Now()
	defer func() {
		result.DurationMs = time.Since(started).Milliseconds()
		logger.Finish(ctx, "RAG", "知识检索", started, err, logger.F("mode", r.Mode), logger.F("top_k", r.TopK), logger.F("count", len(result.Documents)))
	}()
	r.Query = strings.TrimSpace(r.Query)
	if r.Mode == "" {
		r.Mode = s.cfg.Mode
	}
	if r.TopK == 0 {
		r.TopK = s.cfg.TopK
	}
	if r.Query == "" || len(r.Query) > 4096 || !ValidMode(r.Mode) || r.TopK < 1 || r.TopK > 16384 {
		return result, ErrInvalidSearch
	}
	if err = r.Filters.Validate(); err != nil {
		return result, ErrInvalidSearch
	}
	// BM25 and RRF scores have different scales; a COSINE threshold is meaningful only in dense mode.
	if r.DenseMinScore != nil && (r.Mode != Dense || math.IsNaN(*r.DenseMinScore) || math.IsInf(*r.DenseMinScore, 0) || *r.DenseMinScore < -1 || *r.DenseMinScore > 1) {
		return result, ErrInvalidSearch
	}
	threshold := s.cfg.DenseMinScore
	if r.DenseMinScore != nil {
		threshold = *r.DenseMinScore
	}
	scoreType := map[string]string{Dense: "cosine", BM25: "bm25", Hybrid: "rrf"}[r.Mode]
	result = SearchResult{Query: r.Query, Mode: r.Mode, ScoreType: scoreType, Documents: []SearchHit{}}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	limit := min(16384, max(20, 5*r.TopK))
	for {
		docs, cause := s.backend.Candidates(ctx, r.Query, r.Mode, limit, r.Filters)
		if cause != nil {
			return result, searchError(cause)
		}
		ids := []string{}
		for _, d := range docs {
			if d == nil || math.IsNaN(d.Score()) || math.IsInf(d.Score(), 0) {
				return result, ErrSearchFailed
			}
			id, _ := d.MetaData["document_id"].(string)
			ids = append(ids, id)
		}
		active, cause := s.catalog.ActiveVersions(ctx, ids)
		if cause != nil {
			return result, searchError(cause)
		}
		hits := []SearchHit{}
		seen := map[string]bool{}
		for _, d := range docs {
			str := func(key string) string { v, _ := d.MetaData[key].(string); return v }
			id, version := str("document_id"), str("version")
			if version == "" || active[id] != version || seen[d.ID] {
				continue
			}
			if r.Mode == Dense && d.Score() < threshold {
				continue
			}
			seen[d.ID] = true
			hits = append(hits, SearchHit{ID: d.ID, ChunkIndex: chunkIndex(d), DocumentID: id, Version: version, Title: str("title"), Source: str("source"), DocType: str("docType"), SectionPath: str("section_path"), Content: d.Content, Score: d.Score(), ScoreType: scoreType})
		}
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Score == hits[j].Score {
				return hits[i].ID < hits[j].ID
			}
			return hits[i].Score > hits[j].Score
		})
		result.Documents = hits[:min(r.TopK, len(hits))]
		// Staged and obsolete chunks can occupy candidate slots; grow both routes before declaring fewer hits.
		if len(hits) >= r.TopK || len(docs) < limit || limit == 16384 {
			break
		}
		limit = min(16384, limit*2)
	}
	if err = ctx.Err(); err != nil {
		return result, searchError(err)
	}
	return result, nil
}
func searchError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.Join(ErrSearchFailed, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrSearchFailed, context.DeadlineExceeded)
	}
	return ErrSearchFailed
}
func (s *SearchService) Retrieve(ctx context.Context, q string, opts ...retriever.Option) ([]*schema.Document, error) {
	o := retriever.GetCommonOptions(nil, opts...)
	if o.Index != nil || o.SubIndex != nil || o.Embedding != nil || len(o.DSLInfo) > 0 {
		return nil, ErrInvalidSearch
	}
	r := SearchRequest{Query: q, Mode: s.cfg.Mode, DenseMinScore: o.ScoreThreshold}
	if o.TopK != nil {
		r.TopK = *o.TopK
	}
	result, err := s.Search(ctx, r)
	if err != nil {
		return nil, err
	}
	docs := make([]*schema.Document, 0, len(result.Documents))
	for _, h := range result.Documents {
		docs = append(docs, (&schema.Document{ID: h.ID, Content: h.Content, MetaData: map[string]any{"source": h.Source, "title": h.Title, "document_id": h.DocumentID, "version": h.Version, "docType": h.DocType, "section_path": h.SectionPath, "score_type": h.ScoreType, "retrieval_mode": result.Mode}}).WithScore(h.Score))
	}
	return docs, nil
}
func filterExpression(f Filters) string {
	quote := func(v string) string { raw, _ := json.Marshal(v); return string(raw) }
	parts := []string{}
	for _, x := range []struct{ field, value string }{{"doc_type", f.DocType}, {"domain", f.Domain}, {"service", f.Service}} {
		if x.value != "" {
			parts = append(parts, x.field+" == "+quote(x.value))
		}
	}
	for _, tag := range f.Tags {
		parts = append(parts, `JSON_CONTAINS(metadata["tags"], `+quote(tag)+`)`)
	}
	if f.AlertName != "" {
		parts = append(parts, `JSON_CONTAINS(metadata["alertNames"], `+quote(f.AlertName)+`)`)
	}
	return strings.Join(parts, " and ")
}
func (s *Store) Candidates(ctx context.Context, query, mode string, k int, f Filters) ([]*schema.Document, error) {
	if !ValidMode(mode) || k < 1 || k > 16384 {
		return nil, ErrInvalidSearch
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if !s.v2 && mode != Dense {
		return nil, errors.New("BM25 requires a v2 collection; run knowledge_migrate")
	}
	filter := filterExpression(f)
	var v entity.Vector
	if mode != BM25 {
		vectors, err := s.embedding.EmbedStrings(ctx, []string{query})
		if err != nil {
			return nil, err
		}
		if len(vectors) != 1 {
			return nil, ErrSearchFailed
		}
		floats, err := client.FloatVector(vectors[0], s.dimension)
		if err != nil {
			return nil, err
		}
		v = entity.FloatVector(floats)
	}
	var results []milvusclient.ResultSet
	var err error
	if mode == Hybrid {
		db, ok := s.db.(client.HybridMilvus)
		if !ok {
			return nil, ErrSearchFailed
		}
		results, err = db.HybridSearch(ctx, s.cfg.Collection, v, query, filter, k, k)
	} else {
		field, metric, param := "vector", entity.COSINE, index.AnnParam(index.NewHNSWAnnParam(max(64, k)))
		if mode == BM25 {
			field = "sparse_vector"
			metric = entity.BM25
			param = index.NewSparseAnnParam()
			v = entity.Text(query)
		}
		results, err = s.db.Search(ctx, s.cfg.Collection, filter, []string{"content", "metadata"}, []entity.Vector{v}, field, metric, k, param)
	}
	if err != nil {
		return nil, operationError(ctx, "search knowledge", err)
	}
	if len(results) != 1 || results[0].Err != nil {
		return nil, ErrSearchFailed
	}
	r := results[0]
	out := []*schema.Document{}
	if r.ResultCount == 0 {
		return out, nil
	}
	if r.IDs == nil || r.GetColumn("content") == nil || r.GetColumn("metadata") == nil || len(r.Scores) != r.ResultCount {
		return nil, ErrSearchFailed
	}
	for i := 0; i < r.ResultCount; i++ {
		d, err := decodeDocument(r.IDs, r.GetColumn("content"), r.GetColumn("metadata"), i)
		if err != nil {
			return nil, err
		}
		out = append(out, d.WithScore(float64(r.Scores[i])))
	}
	return out, nil
}
