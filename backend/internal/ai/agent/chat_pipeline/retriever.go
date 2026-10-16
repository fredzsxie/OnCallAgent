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
		// Document.String renders Content in the prompt; include citations explicitly.
		result = append(result, &schema.Document{ID: doc.ID, MetaData: doc.MetaData, Content: fmt.Sprintf("\n[Source: %s | Title: %s | ID: %s]\n%s\n", source, title, doc.ID, doc.Content)})
	}
	return result, nil
}
