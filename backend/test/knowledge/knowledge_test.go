package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

type fakeDB struct {
	client.Client
	exists           bool
	schema           *entity.Schema
	index            entity.Index
	loaded           bool
	creates, upserts int
	columns          []entity.Column
	searchMetric     entity.MetricType
	searchParam      entity.SearchParam
	indexErr         error
	deleteExpr       string
}

func (f *fakeDB) HasCollection(context.Context, string) (bool, error) { return f.exists, nil }
func (f *fakeDB) DescribeCollection(context.Context, string) (*entity.Collection, error) {
	return &entity.Collection{Schema: f.schema}, nil
}
func (f *fakeDB) DescribeIndex(_ context.Context, _ string, field string, _ ...client.IndexOption) ([]entity.Index, error) {
	if f.indexErr != nil {
		return nil, f.indexErr
	}
	if field == "vector" && f.index != nil {
		return []entity.Index{f.index}, nil
	}
	return nil, errors.New("index not found")
}
func (f *fakeDB) CreateCollection(_ context.Context, s *entity.Schema, _ int32, _ ...client.CreateCollectionOption) error {
	f.exists = true
	f.schema = s
	f.creates++
	return nil
}
func (f *fakeDB) CreateIndex(_ context.Context, _, _ string, index entity.Index, _ bool, _ ...client.IndexOption) error {
	f.index = index
	f.creates++
	return nil
}
func (f *fakeDB) LoadCollection(context.Context, string, bool, ...client.LoadCollectionOption) error {
	f.loaded = true
	return nil
}
func (f *fakeDB) Upsert(_ context.Context, _, _ string, cols ...entity.Column) (entity.Column, error) {
	f.upserts++
	f.columns = cols
	return cols[0], nil
}
func (f *fakeDB) Delete(ctx context.Context, _ string, _ string, expression string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.deleteExpr = expression
	return nil
}

func (f *fakeDB) Search(ctx context.Context, _ string, _ []string, _ string, fields []string, vectors []entity.Vector, field string, metric entity.MetricType, k int, sp entity.SearchParam, _ ...client.SearchQueryOptionFunc) ([]client.SearchResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if field != "vector" || !reflect.DeepEqual(fields, []string{"content", "metadata"}) || len(vectors) != 1 {
		return nil, errors.New("invalid search arguments")
	}
	f.searchMetric, f.searchParam = metric, sp
	return []client.SearchResult{{ResultCount: 1, IDs: f.columns[0], Fields: client.ResultSet{f.columns[1], f.columns[2]}, Scores: []float32{1}}}, nil
}

type embed struct{ vector []float64 }

func (e embed) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	v := make([][]float64, len(texts))
	for i := range v {
		v[i] = e.vector
	}
	return v, nil
}

func cfg() config.Milvus {
	return config.Milvus{Address: "localhost:19530", Database: "default", Collection: "knowledge_test", ConnectTimeout: time.Second, Timeout: time.Second}
}

func TestInitAndIdempotency(t *testing.T) {
	f := &fakeDB{}
	r, err := knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg())
	if err != nil || r != 3 || !f.loaded || f.creates != 2 || f.index.Params()["metric_type"] != "COSINE" {
		t.Fatalf("initialization failed: %+v, %v", r, err)
	}
	r, err = knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg())
	if err != nil || r != 3 || f.creates != 2 {
		t.Fatal("initialization must not recreate collection or index")
	}
}

func TestInitPreservesMismatchedCollection(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*fakeDB)
	}{
		{"dimension", func(f *fakeDB) { f.schema = knowledge.Schema(cfg().Collection, 4) }},
		{"binary", func(f *fakeDB) { f.schema.Fields[3].DataType = entity.FieldTypeBinaryVector }},
		{"primary_key", func(f *fakeDB) { f.schema.Fields[0].AutoID = true }},
		{"content_length", func(f *fakeDB) { f.schema.Fields[1].TypeParams["max_length"] = "8192" }},
		{"metric", func(f *fakeDB) { f.index, _ = entity.NewIndexHNSW(entity.L2, 16, 128) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeDB{exists: true, schema: knowledge.Schema(cfg().Collection, 3)}
			f.index, _ = entity.NewIndexHNSW(entity.COSINE, 16, 128)
			tt.change(f)
			r, err := knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg())
			if err == nil || r != 0 || f.creates != 0 || f.loaded {
				t.Fatalf("mismatched collection was modified: %+v, %v", r, err)
			}
		})
	}
}

func TestIndexPermissionErrorIsNotMissing(t *testing.T) {
	f := &fakeDB{exists: true, schema: knowledge.Schema(cfg().Collection, 3), indexErr: errors.New("permission denied")}
	if _, err := knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg()); err == nil || f.creates != 0 {
		t.Fatal("permission failure must not trigger DDL")
	}
}

func TestStoreAndCosineRetrieval(t *testing.T) {
	f := &fakeDB{}
	if _, err := knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg()); err != nil {
		t.Fatal(err)
	}
	s, err := knowledge.NewStore(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	doc := &schema.Document{ID: "doc-1", Content: "CPU usage", MetaData: map[string]any{"source": "runbook"}}
	ids, err := s.Store(context.Background(), []*schema.Document{doc})
	if err != nil || ids[0] != "doc-1" || f.columns[3].Type() != entity.FieldTypeFloatVector {
		t.Fatalf("store failed: %v", err)
	}
	v, err := f.columns[3].Get(0)
	if err != nil || !reflect.DeepEqual(v, []float32{1, 2, 3}) {
		t.Fatalf("incorrect float vector: %v %v", v, err)
	}
	raw, _ := f.columns[2].GetAsString(0)
	var metadata map[string]any
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata["source"] != "runbook" {
		t.Fatal("metadata was not preserved")
	}
	got, err := s.Retrieve(context.Background(), "CPU", retriever.WithTopK(100))
	if err != nil || len(got) != 1 || got[0].ID != doc.ID || got[0].MetaData["source"] != "runbook" || got[0].Score() != 1 {
		t.Fatalf("retrieve failed: %+v, %v", got, err)
	}
	if f.searchMetric != entity.COSINE || f.searchParam.Params()["ef"] != 100 {
		t.Fatal("search metric or HNSW params differ")
	}
	got, err = s.Retrieve(context.Background(), "CPU", retriever.WithScoreThreshold(1.1))
	if err != nil || len(got) != 0 {
		t.Fatal("score filtering failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Store(ctx, []*schema.Document{doc}); !errors.Is(err, context.Canceled) {
		t.Fatalf("context lost: %v", err)
	}
}

func TestDimensionMismatchCannotWrite(t *testing.T) {
	f := &fakeDB{}
	if _, err := knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg()); err != nil {
		t.Fatal(err)
	}
	s, err := knowledge.NewStore(context.Background(), f, embed{[]float64{1, 2}}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store(context.Background(), []*schema.Document{{ID: "one", Content: "hello"}})
	if err == nil || f.upserts != 0 {
		t.Fatal("dimension mismatch must be rejected before write")
	}
}

func TestInvalidEmbeddingCannotInitialize(t *testing.T) {
	f := &fakeDB{}
	if _, err := knowledge.Init(context.Background(), f, embed{[]float64{0, 0, 0}}, cfg()); err == nil || f.creates != 0 {
		t.Fatal("invalid embedding must fail before collection mutation")
	}
}

func TestStoreRequiresExplicitInitialization(t *testing.T) {
	f := &fakeDB{}
	if _, err := knowledge.NewStore(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg()); err == nil || f.creates != 0 {
		t.Fatal("store construction must not create a collection")
	}
	f.exists = true
	f.schema = knowledge.Schema(cfg().Collection, 3)
	if _, err := knowledge.NewStore(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg()); err == nil || f.creates != 0 {
		t.Fatal("store construction must not create an index")
	}
}

func TestDeleteOldOnlyTargetsGeneratedHashes(t *testing.T) {
	f := &fakeDB{}
	if _, err := knowledge.Init(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg()); err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.NewStore(context.Background(), f, embed{[]float64{1, 2, 3}}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	documentID, version := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := store.DeleteOld(context.Background(), documentID, version); err != nil {
		t.Fatal(err)
	}
	expected := `metadata["document_id"] == "` + documentID + `" and metadata["version"] != "` + version + `"`
	if f.deleteExpr != expected {
		t.Fatalf("unexpected deletion expression: %s", f.deleteExpr)
	}
	f.deleteExpr = ""
	if err := store.DeleteOld(context.Background(), `unsafe" or true`, version); err == nil || f.deleteExpr != "" {
		t.Fatal("untrusted filter value accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.DeleteOld(ctx, documentID, version); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}
}
