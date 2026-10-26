package testutil

import (
	"context"
	"os"
	"path/filepath"

	knowledgepipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

func NewKnowledgeService(cfg config.Knowledge, store knowledge.DocumentStore) (*knowledge.KnowledgeService, error) {
	if err := os.MkdirAll(cfg.FileDir, 0700); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(cfg.FileDir)
	if err != nil {
		return nil, err
	}
	graph, err := knowledgepipeline.BuildKnowledgeIndexing(context.Background(), knowledgepipeline.Config{Root: root, Knowledge: cfg}, store)
	if err != nil {
		return nil, err
	}
	return knowledge.NewKnowledgeService(cfg, knowledgepipeline.NewFileIndexer(graph))
}
