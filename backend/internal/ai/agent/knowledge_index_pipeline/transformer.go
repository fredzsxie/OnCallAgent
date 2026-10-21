package knowledge_index_pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type markdownSplitter struct{ cfg config.Knowledge }

func newDocumentTransformer(_ context.Context, cfg Config) (document.Transformer, error) {
	if err := cfg.Knowledge.Validate(); err != nil {
		return nil, err
	}
	return &markdownSplitter{cfg: cfg.Knowledge}, nil
}

func (*markdownSplitter) GetType() string { return "MarkdownSplitter" }

func (s *markdownSplitter) Transform(ctx context.Context, docs []*schema.Document, _ ...document.TransformerOption) (out []*schema.Document, err error) {
	started := time.Now()
	defer func() { logger.Finish(ctx, "Split", "文档切分", started, err, logger.F("chunk_count", len(out))) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(docs) != 1 || docs[0] == nil {
		return nil, knowledge.ErrInvalidDocument
	}
	source, _ := docs[0].MetaData["source"].(string)
	source, err = knowledge.NormalizeSource(source)
	if err != nil {
		return nil, err
	}
	// Hash the original bytes, exactly as upload ingestion does before splitting.
	hash := sha256.Sum256([]byte(docs[0].Content))
	return knowledge.Split(source, docs[0].Content, hex.EncodeToString(hash[:]), s.cfg)
}
