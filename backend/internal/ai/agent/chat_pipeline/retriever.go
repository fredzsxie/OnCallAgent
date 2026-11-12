package chat_pipeline

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

type readableRetriever struct{ base retriever.Retriever }

func (r readableRetriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	ctx, cancel, err := executionContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	docs, err := r.base.Retrieve(ctx, query, opts...)
	if err != nil {
		return nil, safeError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, safeError(ctx, err)
	}
	result := make([]*schema.Document, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			return nil, ErrAgentFailed
		}
		source, _ := doc.MetaData["source"].(string)
		title, _ := doc.MetaData["title"].(string)
		section, _ := doc.MetaData["section_path"].(string)
		version, _ := doc.MetaData["version"].(string)
		citation := fmt.Sprintf("\n[Source: %s | Title: %s | ID: %s]\n", source, title, doc.ID)
		if section != "" || version != "" {
			citation += fmt.Sprintf("[Section: %s | Version: %s]\n", section, version)
		}
		// Document.String renders Content only; put versioned section citations in the model-visible text.
		result = append(result, &schema.Document{ID: doc.ID, MetaData: doc.MetaData, Content: citation + doc.Content + "\n"})
	}
	return result, nil
}
