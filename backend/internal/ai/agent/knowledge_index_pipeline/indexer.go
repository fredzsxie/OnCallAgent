package knowledge_index_pipeline

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

type milvusIndexer struct {
	store     knowledge.DocumentStore
	batchSize int
}

func newIndexer(_ context.Context, cfg Config, store knowledge.DocumentStore) (indexer.Indexer, error) {
	if err := cfg.Knowledge.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("document store is required")
	}
	return &milvusIndexer{store: store, batchSize: cfg.Knowledge.BatchSize}, nil
}

func (*milvusIndexer) GetType() string { return "MilvusIndexer" }

func (i *milvusIndexer) Store(ctx context.Context, docs []*schema.Document, _ ...indexer.Option) ([]string, error) {
	if len(docs) == 0 || docs[0] == nil {
		return nil, knowledge.ErrInvalidDocument
	}
	source, _ := docs[0].MetaData["source"].(string)
	if _, err := knowledge.NormalizeSource(source); err != nil {
		return nil, err
	}
	unlock, err := knowledge.LockSource(ctx, source)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return knowledge.IndexChunks(ctx, i.store, docs, i.batchSize)
}
