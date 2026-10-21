package knowledge_index_pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type fileLoader struct{ cfg Config }

type loaderOptions struct{ logicalSource *string }

func WithLogicalSource(source string) document.LoaderOption {
	return document.WrapLoaderImplSpecificOptFn(func(o *loaderOptions) { o.logicalSource = &source })
}

func newLoader(_ context.Context, cfg Config) (document.Loader, error) {
	cfg, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	return &fileLoader{cfg: cfg}, nil
}

func (*fileLoader) GetType() string { return "FileLoader" }

func (l *fileLoader) Load(ctx context.Context, src document.Source, opts ...document.LoaderOption) (out []*schema.Document, err error) {
	started := time.Now()
	defer func() { logger.Finish(ctx, "Loader", "文件读取", started, err, logger.F("documents", len(out))) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if src.URI == "" {
		return nil, knowledge.ErrInvalidDocument
	}
	filename := src.URI
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(l.cfg.Root, filename)
	}
	rel, err := filepath.Rel(l.cfg.Root, filename)
	if err != nil {
		return nil, knowledge.ErrInvalidDocument
	}
	filename = filepath.Join(l.cfg.Root, rel)
	source, err := knowledge.NormalizeSource(filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	// Reject symlinks in every path component, including links that leave the root.
	current := l.cfg.Root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (current == filename && !info.Mode().IsRegular()) {
			return nil, knowledge.ErrInvalidDocument
		}
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, knowledge.ErrInvalidDocument
	}
	data, readErr := knowledge.ReadDocument(ctx, file, l.cfg.Knowledge.MaxUploadBytes)
	if readErr != nil {
		return nil, readErr
	}
	// A versioned physical filename must not change the logical document's identity.
	options := document.GetLoaderImplSpecificOptions[loaderOptions](nil, opts...)
	if options.logicalSource != nil {
		source, err = knowledge.NormalizeSource(*options.logicalSource)
		if err != nil {
			return nil, err
		}
	}
	return []*schema.Document{{Content: string(data), MetaData: map[string]any{"source": source}}}, nil
}
