package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	localclient "github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

type Store struct {
	db        client.Client
	embedding embedding.Embedder
	cfg       config.Milvus
	dimension int
}

var _ indexer.Indexer = (*Store)(nil)
var _ retriever.Retriever = (*Store)(nil)

func NewStore(ctx context.Context, db client.Client, emb embedding.Embedder, cfg config.Milvus, dimension int) (*Store, error) {
	if db == nil || emb == nil || dimension < 1 {
		return nil, errors.New("Milvus, embedding and a positive dimension are required")
	}
	r, err := Check(ctx, db, cfg, dimension)
	if err != nil {
		return nil, err
	}
	if !r.Ready {
		return nil, errors.New("knowledge collection is incompatible or not loaded; run milvus_check / init_knowledge")
	}
	return &Store{db, emb, cfg, dimension}, nil
}

func NewID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (s *Store) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	options := indexer.GetCommonOptions(nil, opts...)
	if len(options.SubIndexes) != 0 || options.Embedding != nil {
		return nil, errors.New("sub-index and embedder overrides are unsupported")
	}
	if len(docs) == 0 {
		return []string{}, nil
	}
	ids, texts, metadata := make([]string, len(docs)), make([]string, len(docs)), make([][]byte, len(docs))
	seen := map[string]bool{}
	for i, doc := range docs {
		if doc == nil || strings.TrimSpace(doc.Content) == "" || len(doc.Content) > ContentMaxLength {
			return nil, errors.New("document content must be nonempty and at most 65535 UTF-8 bytes")
		}
		ids[i], texts[i] = doc.ID, doc.Content
		if ids[i] == "" {
			var err error
			if ids[i], err = NewID(); err != nil {
				return nil, err
			}
		}
		if len(ids[i]) > IDMaxLength || seen[ids[i]] {
			return nil, errors.New("document IDs must be unique in a batch and at most 256 bytes")
		}
		seen[ids[i]] = true
		m := doc.MetaData
		if m == nil {
			m = map[string]any{}
		}
		var err error
		if metadata[i], err = json.Marshal(m); err != nil {
			return nil, errors.New("document metadata must be JSON serializable")
		}
		if len(metadata[i]) > 65536 {
			return nil, errors.New("document metadata exceeds 65536 bytes")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	vectors, err := s.embedding.EmbedStrings(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(docs) {
		return nil, errors.New("embedding response count does not match documents")
	}
	values := make([][]float32, len(vectors))
	// FLOAT_VECTOR stores numeric float32 values, never float bytes as BINARY_VECTOR.
	for i, vector := range vectors {
		if values[i], err = localclient.FloatVector(vector, s.dimension); err != nil {
			return nil, err
		}
	}
	_, err = s.db.Upsert(ctx, s.cfg.Collection, "",
		entity.NewColumnVarChar("id", ids), entity.NewColumnVarChar("content", texts),
		entity.NewColumnJSONBytes("metadata", metadata), entity.NewColumnFloatVector("vector", s.dimension, values))
	if err != nil {
		return nil, operationError(ctx, "upsert documents", err)
	}
	return ids, nil
}

func (s *Store) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	options := retriever.GetCommonOptions(nil, opts...)
	if options.Index != nil || options.SubIndex != nil || options.Embedding != nil || len(options.DSLInfo) != 0 {
		return nil, errors.New("index, sub-index, embedder and DSL overrides are unsupported")
	}
	topK := 5
	if options.TopK != nil {
		topK = *options.TopK
	}
	if topK < 1 || topK > 16384 {
		return nil, errors.New("top_k must be between 1 and 16384")
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	vectors, err := s.embedding.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, errors.New("expected one query embedding")
	}
	vector, err := localclient.FloatVector(vectors[0], s.dimension)
	if err != nil {
		return nil, err
	}
	ef := 64
	if topK > ef {
		ef = topK
	}
	searchParam, err := entity.NewIndexHNSWSearchParam(ef)
	if err != nil {
		return nil, err
	}
	// Strong consistency makes a completed write visible to the following verification search.
	results, err := s.db.Search(ctx, s.cfg.Collection, nil, "", []string{"content", "metadata"}, []entity.Vector{entity.FloatVector(vector)}, "vector", entity.COSINE, topK, searchParam, client.WithSearchQueryConsistencyLevel(entity.ClStrong))
	if err != nil {
		return nil, operationError(ctx, "search vectors", err)
	}
	if len(results) != 1 || results[0].Err != nil {
		return nil, errors.New("invalid Milvus search result")
	}
	r := results[0]
	docs := make([]*schema.Document, 0, r.ResultCount)
	if r.ResultCount == 0 {
		return docs, nil
	}
	content, metadata := r.Fields.GetColumn("content"), r.Fields.GetColumn("metadata")
	if r.IDs == nil || content == nil || metadata == nil || len(r.Scores) != r.ResultCount {
		return nil, errors.New("Milvus result fields are incomplete")
	}
	for i := 0; i < r.ResultCount; i++ {
		if options.ScoreThreshold != nil && float64(r.Scores[i]) < *options.ScoreThreshold {
			continue
		}
		id, err := r.IDs.GetAsString(i)
		if err != nil {
			return nil, errors.New("invalid result ID")
		}
		text, err := content.GetAsString(i)
		if err != nil {
			return nil, errors.New("invalid result content")
		}
		raw, err := metadata.GetAsString(i)
		if err != nil {
			return nil, errors.New("invalid result metadata")
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
			return nil, errors.New("invalid result metadata JSON")
		}
		docs = append(docs, (&schema.Document{ID: id, Content: text, MetaData: m}).WithScore(float64(r.Scores[i])))
	}
	return docs, nil
}
