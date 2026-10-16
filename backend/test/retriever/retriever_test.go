package retriever_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type backend struct {
	docs    []*schema.Document
	err     error
	calls   int
	query   string
	ctx     context.Context
	options *eino.Options
}

func (b *backend) Retrieve(ctx context.Context, query string, opts ...eino.Option) ([]*schema.Document, error) {
	b.calls++
	b.ctx, b.query, b.options = ctx, query, eino.GetCommonOptions(nil, opts...)
	return b.docs, b.err
}

func newRetriever(t *testing.T, b *backend) *retriever.MilvusRetriever {
	t.Helper()
	r, err := retriever.NewMilvusRetriever(b, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPolicyAndStructuredResults(t *testing.T) {
	b := &backend{}
	for i, score := range []float64{0.7, 0.9, 0.4, 0.5, 0.8} {
		b.docs = append(b.docs, (&schema.Document{ID: string(rune('a' + i)), Content: "body", MetaData: map[string]any{"source": "runbook.md", "title": "Recovery", "chunk_index": i}}).WithScore(score))
	}
	r := newRetriever(t, b)
	ctx := context.WithValue(context.Background(), struct{}{}, "request")
	docs, err := r.Retrieve(ctx, "  CPU alarm \n")
	if err != nil || len(docs) != 3 || docs[0].ID != "b" || docs[1].ID != "e" || docs[2].ID != "a" {
		t.Fatalf("policy result: %+v %v", docs, err)
	}
	if b.ctx != ctx || b.query != "CPU alarm" || *b.options.TopK != 3 || *b.options.ScoreThreshold != 0.5 {
		t.Fatal("default options/query/context not propagated")
	}
	if !reflect.DeepEqual(docs[0].MetaData, b.docs[1].MetaData) || b.docs[0].ID != "a" {
		t.Fatal("metadata or backend slice modified")
	}
	docs, err = r.Retrieve(ctx, "CPU", eino.WithTopK(4), eino.WithScoreThreshold(-1))
	if err != nil || len(docs) != 4 || *b.options.TopK != 4 || *b.options.ScoreThreshold != -1 {
		t.Fatal("call overrides ignored")
	}
	docs, err = r.Retrieve(ctx, "CPU", eino.WithTopK(5), eino.WithScoreThreshold(0.5))
	if err != nil || len(docs) != 4 || docs[3].Score() != 0.5 {
		t.Fatal("threshold boundary excluded")
	}
	result, err := r.Search(ctx, " CPU ")
	if err != nil || result.Query != "CPU" || result.Documents[0].Source != "runbook.md" || result.Documents[0].Title != "Recovery" || result.Documents[0].Score != 0.9 {
		t.Fatalf("structured result: %+v %v", result, err)
	}
	b.docs = []*schema.Document{(&schema.Document{ID: "legacy", Content: "body"}).WithScore(0.8)}
	result, err = r.Search(ctx, "legacy")
	if err != nil || result.Documents[0].Source != "" || result.Documents[0].Title != "" {
		t.Fatal("missing optional metadata was not tolerated")
	}
}

func TestNoResultsAndFailuresAreDistinct(t *testing.T) {
	b := &backend{}
	r := newRetriever(t, b)
	for _, docs := range [][]*schema.Document{nil, {(&schema.Document{ID: "low"}).WithScore(0.49)}} {
		b.docs = docs
		result, err := r.Search(context.Background(), "unrelated")
		data, marshalErr := json.Marshal(result)
		if err != nil || marshalErr != nil || len(result.Documents) != 0 || !strings.Contains(string(data), `"documents":[]`) {
			t.Fatalf("empty result became an error/null: %s %v", data, err)
		}
	}
	for _, err := range []error{errors.New("fixture-secret: access denied"), context.Canceled, context.DeadlineExceeded} {
		b.err = errors.Join(errors.New("fixture-secret"), err)
		_, got := r.Search(context.Background(), "CPU")
		if !errors.Is(got, retriever.ErrQueryFailed) || strings.Contains(got.Error(), "fixture-secret") {
			t.Fatalf("unsafe failure: %v", got)
		}
		if (err == context.Canceled || err == context.DeadlineExceeded) && !errors.Is(got, err) {
			t.Fatal("context error lost")
		}
	}
	b.err = nil
	for _, doc := range []*schema.Document{nil, (&schema.Document{}).WithScore(math.NaN()), (&schema.Document{}).WithScore(math.Inf(1))} {
		b.docs = []*schema.Document{doc}
		if _, err := r.Search(context.Background(), "CPU"); !errors.Is(err, retriever.ErrQueryFailed) {
			t.Fatal("invalid backend document accepted")
		}
	}
}

func TestValidationStopsBackend(t *testing.T) {
	b := &backend{}
	r := newRetriever(t, b)
	if _, err := r.Search(context.Background(), " \n"); !errors.Is(err, retriever.ErrInvalidQuery) {
		t.Fatal("empty query accepted")
	}
	for _, option := range []eino.Option{eino.WithTopK(0), eino.WithTopK(16385), eino.WithScoreThreshold(math.NaN()), eino.WithScoreThreshold(1.1), eino.WithIndex("other"), eino.WithSubIndex("other"), eino.WithDSLInfo(map[string]any{"filter": "x"})} {
		if _, err := r.Retrieve(context.Background(), "CPU", option); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Search(ctx, "CPU"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	if b.calls != 0 {
		t.Fatal("invalid request called backend")
	}
	if _, err := retriever.NewMilvusRetriever(nil, config.Retrieval{TopK: 3}); err == nil {
		t.Fatal("nil dependency accepted")
	}
	if _, err := retriever.NewMilvusRetriever(b, config.Retrieval{}); err == nil {
		t.Fatal("invalid config accepted")
	}
}
