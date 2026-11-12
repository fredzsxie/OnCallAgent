package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
)

type Store struct {
	db        client.Milvus
	embedding embedding.Embedder
	cfg       config.Milvus
	dimension int
	v2        bool
}

var _ indexer.Indexer = (*Store)(nil)
var _ retriever.Retriever = (*Store)(nil)

func NewStore(ctx context.Context, db client.Milvus, emb embedding.Embedder, cfg config.Milvus) (*Store, error) {
	if db == nil || emb == nil {
		return nil, errors.New("Milvus and embedding are required")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	collection, err := db.DescribeCollection(ctx, cfg.Collection)
	if err != nil {
		return nil, operationError(ctx, "describe collection; run init_knowledge first", err)
	}
	dimension := VectorDimension(collection.Schema)
	if dimension < 1 {
		return nil, errors.New("collection must contain a FLOAT_VECTOR field with a positive dimension")
	}
	wanted := Schema(cfg.Collection, dimension)
	v2 := isV2(collection.Schema)
	if v2 {
		wanted = SchemaV2(cfg.Collection, dimension)
	}
	if diff := schemaDiff(collection.Schema, wanted); len(diff) != 0 {
		return nil, fmt.Errorf("existing collection differs: %s", strings.Join(diff, "; "))
	}
	hasIndex, err := vectorIndex(ctx, db, cfg.Collection)
	if err != nil {
		return nil, err
	}
	if !hasIndex {
		return nil, errors.New("vector index is missing; run init_knowledge first")
	}
	logger.Info(ctx, "Milvus", "集合校验完成", logger.F("dimension", dimension), logger.F("metric", "COSINE"))
	if v2 {
		ok, err := checkIndex(ctx, db, cfg.Collection, "sparse_vector", "SPARSE_INVERTED_INDEX", "BM25")
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("BM25 index is missing; run init_knowledge first")
		}
	}
	return &Store{db: db, embedding: emb, cfg: cfg, dimension: dimension, v2: v2}, nil
}

func NewID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (s *Store) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) (out []string, err error) {
	started := time.Now()
	stage := "validate"
	defer func() {
		logger.Finish(ctx, "Milvus", "向量写入", started, err, logger.F("stage", stage), logger.F("count", len(docs)))
	}()
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
	embedTexts := append([]string(nil), texts...)
	if s.v2 {
		for i, d := range docs {
			if text, ok := d.MetaData["search_text"].(string); ok && text != "" {
				if len(text) > ContentMaxLength {
					return nil, ErrInvalidDocument
				}
				embedTexts[i] = text
			}
		}
	}
	stage = "embedding"
	vectors, err := s.embedding.EmbedStrings(ctx, embedTexts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(docs) {
		return nil, errors.New("embedding response count does not match documents")
	}
	values := make([][]float32, len(vectors))
	// FLOAT_VECTOR stores numeric float32 values, never float bytes as BINARY_VECTOR.
	for i, vector := range vectors {
		if values[i], err = client.FloatVector(vector, s.dimension); err != nil {
			return nil, err
		}
	}
	stage = "upsert"
	cols := []column.Column{column.NewColumnVarChar("id", ids), column.NewColumnVarChar("content", texts), column.NewColumnJSONBytes("metadata", metadata), column.NewColumnFloatVector("vector", s.dimension, values)}
	if s.v2 {
		cols = append(cols, v2Columns(docs, embedTexts)...)
	}
	written, err := s.db.Upsert(ctx, s.cfg.Collection, cols...)
	if err != nil {
		return nil, operationError(ctx, "upsert documents", err)
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

func (s *Store) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (out []*schema.Document, err error) {
	started := time.Now()
	stage := "validate"
	defer func() {
		logger.Finish(ctx, "Milvus", "向量检索", started, err, logger.F("stage", stage), logger.F("count", len(out)))
	}()
	options := retriever.GetCommonOptions(nil, opts...)
	if options.Index != nil || options.SubIndex != nil || options.Embedding != nil || len(options.DSLInfo) != 0 {
		return nil, errors.New("index, sub-index, embedder and DSL overrides are unsupported")
	}
	topK := config.DefaultRetrievalTopK
	if options.TopK != nil {
		topK = *options.TopK
	}
	if topK < 1 || topK > 16384 {
		return nil, errors.New("top_k must be between 1 and 16384")
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	stage = "embedding"
	vectors, err := s.embedding.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, errors.New("expected one query embedding")
	}
	vector, err := client.FloatVector(vectors[0], s.dimension)
	if err != nil {
		return nil, err
	}
	ef := 64
	if topK > ef {
		ef = topK
	}
	searchParam := index.NewHNSWAnnParam(ef)
	// Strong consistency makes a completed write visible to the following verification search.
	stage = "search"
	results, err := s.db.Search(ctx, s.cfg.Collection, "", []string{"content", "metadata"}, []entity.Vector{entity.FloatVector(vector)}, "vector", entity.COSINE, topK, searchParam)
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
	content, metadata := r.GetColumn("content"), r.GetColumn("metadata")
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
		raw, err := client.JSONAt(metadata, i)
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

func (s *Store) DeleteOld(ctx context.Context, documentID, version string) (err error) {
	started := time.Now()
	defer func() {
		logger.Finish(ctx, "Milvus", "旧版本清理", started, err, logger.F("document_hash", documentID))
	}()
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
	if err := s.db.Delete(ctx, s.cfg.Collection, expression); err != nil {
		return operationError(ctx, "delete old document chunks", err)
	}
	return nil
}

func (s *Store) HybridEnabled() bool { return s.v2 }
func (s *Store) Profile(model string) string {
	return fmt.Sprintf("%s/%s/%s:%d:jieba-lowercase:v2", s.cfg.Database, s.cfg.Collection, model, s.dimension)
}
func v2Columns(docs []*schema.Document, texts []string) []column.Column {
	fields := map[string][]string{}
	for _, name := range []string{"document_id", "version", "doc_type", "domain", "service"} {
		fields[name] = make([]string, len(docs))
	}
	indexes := make([]int64, len(docs))
	for i, d := range docs {
		for name := range fields {
			key := name
			if key == "doc_type" {
				key = "docType"
			}
			fields[name][i], _ = d.MetaData[key].(string)
		}
		if fields["doc_type"][i] == "" {
			fields["doc_type"][i] = "reference"
		}
		switch n := d.MetaData["chunk_index"].(type) {
		case int:
			indexes[i] = int64(n)
		case float64:
			indexes[i] = int64(n)
		case int64:
			indexes[i] = n
		}
	}
	cols := []column.Column{}
	for _, name := range []string{"document_id", "version", "doc_type", "domain", "service"} {
		cols = append(cols, column.NewColumnVarChar(name, fields[name]))
	}
	return append(cols, column.NewColumnInt64("chunk_index", indexes), column.NewColumnVarChar("search_text", texts))
}

func validHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}
func decodeDocument(ids, contents, metas column.Column, i int) (*schema.Document, error) {
	id, err := ids.GetAsString(i)
	if err != nil {
		return nil, ErrInvalidDocument
	}
	text, err := contents.GetAsString(i)
	if err != nil {
		return nil, ErrInvalidDocument
	}
	raw, err := client.JSONAt(metas, i)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err = json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, ErrInvalidDocument
	}
	return &schema.Document{ID: id, Content: text, MetaData: m}, nil
}
