package tools

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
)

const QueryInternalDocs = "query_internal_docs"
const queryDescription = "Search CDN terminology, alert SOPs and reference documentation. Results contain content, source, title, section, version and mode-specific ranking scores (COSINE, BM25 or RRF). An empty documents array means no matching evidence was found."

type QueryInternalDocsInput struct {
	Query string `json:"query" jsonschema:"required,description=The question or operational procedure to search for"`
}

func NewQueryInternalDocsTool(r *retriever.MilvusRetriever) (tool.InvokableTool, error) {
	if r == nil {
		return nil, errors.New("document retriever is required")
	}
	return utils.InferTool(QueryInternalDocs, queryDescription, func(ctx context.Context, input *QueryInternalDocsInput) (retriever.Result, error) {
		if input == nil {
			return retriever.Result{}, retriever.ErrInvalidQuery
		}
		return r.Search(ctx, input.Query)
	})
}
