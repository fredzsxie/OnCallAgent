package knowledge_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

type searchFixture struct {
	docs  []*schema.Document
	calls []int
	mode  string
	err   error
}

func (f *searchFixture) Candidates(ctx context.Context, q, mode string, k int, filters knowledge.Filters) ([]*schema.Document, error) {
	f.calls = append(f.calls, k)
	f.mode = mode
	return f.docs[:min(k, len(f.docs))], errors.Join(ctx.Err(), f.err)
}
func TestSearchFiltersUnpublishedVersionsAndExpandsCandidates(t *testing.T) {
	ctx := context.Background()
	c, err := knowledge.OpenCatalog(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	id, err := c.Stage(ctx, "test.md", "active", knowledge.Metadata{Title: "CDN", DocType: "reference"}, "", true, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Publish(ctx, id, "active", 1); err != nil {
		t.Fatal(err)
	}
	f := &searchFixture{}
	for i := 0; i < 20; i++ {
		f.docs = append(f.docs, (&schema.Document{ID: fmt.Sprint(i), Content: "staged", MetaData: map[string]any{"document_id": id, "version": "staging"}}).WithScore(1))
	}
	f.docs = append(f.docs, (&schema.Document{ID: "live", Content: "回源", MetaData: map[string]any{"document_id": id, "version": "active", "title": "CDN", "source": "test.md"}}).WithScore(0.01))
	s, err := knowledge.NewSearchService(c, f, knowledge.SearchConfig{Mode: "hybrid", TopK: 3, DenseMinScore: 0.5, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Search(ctx, knowledge.SearchRequest{Query: "回源", TopK: 1})
	if err != nil || len(r.Documents) != 1 || r.Documents[0].ID != "live" || len(f.calls) != 2 || r.ScoreType != "rrf" {
		t.Fatalf("valid result lost: %+v %+v %v", r, f.calls, err)
	}
	r, err = s.Search(ctx, knowledge.SearchRequest{Query: "回源", Mode: "dense", TopK: 1})
	if err != nil || len(r.Documents) != 0 {
		t.Fatal("COSINE threshold not applied")
	}
	threshold := 0.5
	if _, err = s.Search(ctx, knowledge.SearchRequest{Query: "回源", Mode: "bm25", DenseMinScore: &threshold}); !errors.Is(err, knowledge.ErrInvalidSearch) {
		t.Fatal("incompatible score threshold accepted")
	}
	f.docs[0].WithScore(math.NaN())
	if _, err = s.Search(ctx, knowledge.SearchRequest{Query: "回源"}); !errors.Is(err, knowledge.ErrSearchFailed) {
		t.Fatal("invalid score accepted")
	}
}

type lexicalDB struct {
	fakeDB
	hybridCalls int
}

func (d *lexicalDB) DescribeIndex(ctx context.Context, collection, field string) ([]index.Index, error) {
	if field == "sparse_vector" {
		return []index.Index{index.NewSparseInvertedIndex(entity.BM25, 0)}, nil
	}
	return d.fakeDB.DescribeIndex(ctx, collection, field)
}
func (d *lexicalDB) Search(ctx context.Context, collection, filter string, fields []string, vectors []entity.Vector, field string, metric entity.MetricType, k int, param index.AnnParam) ([]milvusclient.ResultSet, error) {
	if field != "sparse_vector" || metric != entity.BM25 || len(vectors) != 1 || vectors[0] != entity.Text("HTTP_5XX") {
		return nil, errors.New("incorrect lexical query")
	}
	return []milvusclient.ResultSet{{ResultCount: 0}}, ctx.Err()
}
func (d *lexicalDB) HybridSearch(ctx context.Context, collection string, v entity.Vector, q, filter string, candidates, limit int) ([]milvusclient.ResultSet, error) {
	d.hybridCalls++
	return []milvusclient.ResultSet{{}}, ctx.Err()
}

type countedEmbedding struct{ calls int }

func (e *countedEmbedding) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	e.calls++
	return [][]float64{{1, 2, 3}}, ctx.Err()
}
func TestBM25DoesNotCallEmbedding(t *testing.T) {
	d := &lexicalDB{fakeDB: fakeDB{exists: true, schema: knowledge.SchemaV2("v2", 3), index: index.NewHNSWIndex(entity.COSINE, 16, 128)}}
	e := &countedEmbedding{}
	s, err := knowledge.NewStore(context.Background(), d, e, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Candidates(context.Background(), "HTTP_5XX", "bm25", 20, knowledge.Filters{}); err != nil || e.calls != 0 {
		t.Fatalf("BM25 used embeddings: %d %v", e.calls, err)
	}
	if _, err = s.Candidates(context.Background(), "HTTP_5XX", "hybrid", 20, knowledge.Filters{}); err != nil || e.calls != 1 || d.hybridCalls != 1 {
		t.Fatalf("hybrid route missing: %v", err)
	}
}
