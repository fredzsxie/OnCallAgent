package knowledge_index_pipeline

import (
	"context"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/compose"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

func NewFileIndexer(graph compose.Runnable[document.Source, []string]) knowledge.IndexFileFunc {
	if graph == nil {
		return nil
	}
	return func(ctx context.Context, file document.Source, logicalSource string) ([]string, error) {
		return graph.Invoke(ctx, file, compose.WithLoaderOption(WithLogicalSource(logicalSource)).DesignateNode("FileLoader"))
	}
}
