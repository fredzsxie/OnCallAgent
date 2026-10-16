package testutil

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
)

type MemoryStore struct {
	mu          sync.Mutex
	Docs        map[string]*schema.Document
	Calls       int
	BatchSizes  []int
	Events      []string
	FailBatch   int
	FailDelete  bool
	BadIDs      bool
	BeforeStore func(context.Context) error
}

func (m *MemoryStore) Store(ctx context.Context, docs []*schema.Document, _ ...indexer.Option) ([]string, error) {
	if m.BeforeStore != nil {
		if err := m.BeforeStore(ctx); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Docs == nil {
		m.Docs = map[string]*schema.Document{}
	}
	m.Calls++
	m.BatchSizes = append(m.BatchSizes, len(docs))
	m.Events = append(m.Events, "upsert")
	if m.Calls == m.FailBatch {
		if len(docs) > 0 {
			m.Docs[docs[0].ID] = docs[0]
		}
		return nil, errors.New("fixture-secret: upstream write failed")
	}
	ids := make([]string, len(docs))
	for i, doc := range docs {
		m.Docs[doc.ID] = doc
		ids[i] = doc.ID
	}
	if m.BadIDs {
		return nil, nil
	}
	return ids, nil
}

func (m *MemoryStore) DeleteOld(ctx context.Context, documentID, version string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, "delete")
	if m.FailDelete {
		return errors.New("fixture-secret: cleanup failed")
	}
	for id, doc := range m.Docs {
		if doc.MetaData["document_id"] == documentID && doc.MetaData["version"] != version {
			delete(m.Docs, id)
		}
	}
	return nil
}

func (m *MemoryStore) Snapshot() []*schema.Document {
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := make([]*schema.Document, 0, len(m.Docs))
	for _, doc := range m.Docs {
		docs = append(docs, doc)
	}
	return docs
}
