package knowledge_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

func TestQueryUsesIndexingVectorSpaceAndDefaultTopK(t *testing.T) {
	ctx := context.Background()
	f := &fakeDB{}
	if _, err := knowledge.Init(ctx, f, embed{[]float64{1, 2, 3}}, cfg()); err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.NewStore(ctx, f, embed{[]float64{1, 2, 3}}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Store(ctx, []*schema.Document{{ID: "one", Content: "CPU recovery", MetaData: map[string]any{"source": "cpu.md", "title": "Recovery"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Retrieve(ctx, "CPU"); err != nil {
		t.Fatal(err)
	}
	if f.searchK != 3 || f.searchMetric != entity.COSINE || f.searchParam.Params()["ef"] != 64 || !reflect.DeepEqual(f.searchVectors, []entity.Vector{entity.FloatVector{1, 2, 3}}) {
		t.Fatal("query vector conversion, TopK, metric or index parameters differ")
	}
	r, err := retriever.NewMilvusRetriever(store, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Search(ctx, "CPU")
	if err != nil || len(result.Documents) != 1 || result.Documents[0].Source != "cpu.md" || result.Documents[0].Score != 1 {
		t.Fatalf("SDK adapter result differs: %+v %v", result, err)
	}
	calls := f.searchCalls
	if _, err := r.Retrieve(ctx, "CPU", eino.WithEmbedding(embed{[]float64{2, 3, 4}})); err == nil || f.searchCalls != calls {
		t.Fatal("query embedder override reached search")
	}
	wrongStore, err := knowledge.NewStore(ctx, f, embed{[]float64{1, 2}}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := retriever.NewMilvusRetriever(wrongStore, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Search(ctx, "CPU"); !errors.Is(err, retriever.ErrQueryFailed) || f.searchCalls != calls {
		t.Fatal("mismatched query dimension reached Milvus")
	}
}
