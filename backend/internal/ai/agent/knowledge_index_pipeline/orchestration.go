package knowledge_index_pipeline

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/compose"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

// Generated with Eino Dev 1.3.0 nclc; adapted for injected dependencies and checked graph operations.
func BuildKnowledgeIndexing(ctx context.Context, cfg Config, store knowledge.DocumentStore) (r compose.Runnable[document.Source, []string], err error) {
	started := time.Now()
	defer func() { logger.Finish(ctx, "Graph", "图编译", started, err, logger.F("graph", "KnowledgeIndexing")) }()
	const (
		FileLoader       = "FileLoader"
		MarkdownSplitter = "MarkdownSplitter"
		MilvusIndexer    = "MilvusIndexer"
	)
	g := compose.NewGraph[document.Source, []string]()
	loader, err := newLoader(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = g.AddLoaderNode(FileLoader, loader, compose.WithNodeName("FileLoader")); err != nil {
		return nil, err
	}
	splitter, err := newDocumentTransformer(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = g.AddDocumentTransformerNode(MarkdownSplitter, splitter, compose.WithNodeName("MarkdownSplitter")); err != nil {
		return nil, err
	}
	idx, err := newIndexer(ctx, cfg, store)
	if err != nil {
		return nil, err
	}
	if err = g.AddIndexerNode(MilvusIndexer, idx, compose.WithNodeName("MilvusIndexer")); err != nil {
		return nil, err
	}
	if err = g.AddEdge(compose.START, FileLoader); err != nil {
		return nil, err
	}
	if err = g.AddEdge(FileLoader, MarkdownSplitter); err != nil {
		return nil, err
	}
	if err = g.AddEdge(MarkdownSplitter, MilvusIndexer); err != nil {
		return nil, err
	}
	if err = g.AddEdge(MilvusIndexer, compose.END); err != nil {
		return nil, err
	}
	r, err = g.Compile(ctx, compose.WithGraphName("KnowledgeIndexing"), compose.WithNodeTriggerMode(compose.AnyPredecessor))
	if err != nil {
		return nil, err
	}
	return r, err
}
