package knowledge

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	localclient "github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

func ProbeDimension(ctx context.Context, emb embedding.Embedder) (int, error) {
	vectors, err := emb.EmbedStrings(ctx, []string{"OnCallAgent embedding dimension check"})
	if err != nil {
		return 0, err
	}
	if len(vectors) != 1 {
		return 0, errors.New("dimension probe expected one embedding")
	}
	dim := len(vectors[0])
	if _, err := localclient.FloatVector(vectors[0], dim); err != nil {
		return 0, err
	}
	return dim, nil
}

type Verification struct {
	ID             string  `json:"id"`
	Dimension      int     `json:"dimension"`
	Metric         string  `json:"metric"`
	Score          float64 `json:"score"`
	Matched        bool    `json:"matched"`
	RecordRetained bool    `json:"record_retained"`
}

func Verify(ctx context.Context, store *Store) (Verification, error) {
	id, err := NewID()
	if err != nil {
		return Verification{}, err
	}
	id = "check_" + id
	r := Verification{ID: id, Dimension: store.dimension, Metric: "COSINE"}
	doc := &schema.Document{ID: id, Content: "OnCallAgent 向量写入与检索验收，记录编号：" + id, MetaData: map[string]any{"source": "knowledge_check", "acceptance": true}}
	if _, err := store.Store(ctx, []*schema.Document{doc}); err != nil {
		return r, err
	}
	r.RecordRetained = true
	docs, err := store.Retrieve(ctx, doc.Content)
	if err != nil {
		return r, err
	}
	for _, found := range docs {
		if found.ID == id && found.Content == doc.Content && found.MetaData["source"] == "knowledge_check" && found.Score() >= 0.99 {
			r.Matched, r.Score = true, found.Score()
			return r, nil
		}
	}
	return r, errors.New("newly written verification record did not match the COSINE search")
}
