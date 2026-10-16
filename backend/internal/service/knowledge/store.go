package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func NewStore(ctx context.Context, db client.Client, emb embedding.Embedder, cfg config.Milvus) (*Store, error) {
	if db == nil || emb == nil {
		return nil, errors.New("Milvus and embedding are required")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	collection, err := db.DescribeCollection(ctx, cfg.Collection)
	if err != nil {
		return nil, operationError(ctx, "describe collection; run init_knowledge first")
	}
	dimension := VectorDimension(collection.Schema)
	if dimension < 1 {
		return nil, errors.New("collection must contain a FLOAT_VECTOR field with a positive dimension")
	}
	if diff := SchemaDiff(collection.Schema, dimension); len(diff) != 0 {
		return nil, fmt.Errorf("existing collection differs: %s", strings.Join(diff, "; "))
	}
	hasIndex, err := vectorIndex(ctx, db, cfg.Collection)
	if err != nil {
		return nil, err
	}
	if !hasIndex {
		return nil, errors.New("vector index is missing; run init_knowledge first")
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
		if len(ids[i]) > IDMaxLength {
			return nil, errors.New("document IDs must be at most 256 bytes")
		}
		m := doc.MetaData
		if m == nil {
			m = map[string]any{}
		}
		var err error
		if metadata[i], err = json.Marshal(m); err != nil {
			return nil, errors.New("document metadata must be JSON serializable")
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
	written, err := s.db.Upsert(ctx, s.cfg.Collection, "",
		entity.NewColumnVarChar("id", ids), entity.NewColumnVarChar("content", texts),
		entity.NewColumnJSONBytes("metadata", metadata), entity.NewColumnFloatVector("vector", s.dimension, values))
	if err != nil {
		return nil, operationError(ctx, "upsert documents")
	}
	if written == nil || written.Len() != len(ids) {
		return nil, errors.New("Milvus returned an incomplete upsert result")
	}
	for i, id := range ids {
		actual, err := written.GetAsString(i)
		if err != nil || actual != id {
			return nil, errors.New("Milvus returned unexpected upsert IDs")
		}
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
		return nil, operationError(ctx, "search vectors")
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

func (s *Store) DeleteOld(ctx context.Context, documentID, version string) error {
	for _, value := range []string{documentID, version} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return errors.New("document ID and version must be SHA-256 hashes")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	// Only generated hashes enter the filter, never filenames or document text.
	expression := fmt.Sprintf(`metadata["document_id"] == "%s" and metadata["version"] != "%s"`, documentID, version)
	if err := s.db.Delete(ctx, s.cfg.Collection, "", expression); err != nil {
		return operationError(ctx, "delete old document chunks")
	}
	return nil
}
