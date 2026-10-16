package knowledge

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

var sourceLocks sync.Map

func NormalizeSource(source string) (string, error) {
	source = strings.ReplaceAll(source, "\\", "/")
	if !utf8.ValidString(source) || len(source) > 4096 || source == "" || path.IsAbs(source) || strings.ContainsAny(source, "\x00\r\n") {
		return "", ErrInvalidDocument
	}
	for _, part := range strings.Split(source, "/") {
		if part == ".." {
			return "", ErrInvalidDocument
		}
	}
	source = path.Clean(source)
	if !Supported(source) {
		return "", ErrUnsupportedFormat
	}
	return source, nil
}

// Share source locks across upload services and graph instances in this process.
func LockSource(ctx context.Context, source string) (func(), error) {
	value, _ := sourceLocks.LoadOrStore(digest(source), make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, errors.Join(ErrRetryable, ctx.Err())
	}
}

func ReadDocument(ctx context.Context, input io.Reader, maxBytes int64) ([]byte, error) {
	if input == nil || maxBytes < 1 {
		return nil, ErrInvalidDocument
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrRetryable, err)
	}
	data, err := io.ReadAll(io.LimitReader(input, maxBytes+1))
	if err != nil {
		return nil, errors.Join(ErrRetryable, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrRetryable, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff")) == "" {
		return nil, ErrInvalidDocument
	}
	return data, nil
}

func IndexChunks(ctx context.Context, store DocumentStore, docs []*schema.Document, batchSize int) ([]string, error) {
	if store == nil || len(docs) == 0 || docs[0] == nil || batchSize < 1 {
		return nil, ErrInvalidDocument
	}
	documentID, _ := docs[0].MetaData["document_id"].(string)
	version, _ := docs[0].MetaData["version"].(string)
	for _, doc := range docs {
		if doc == nil || doc.ID == "" || documentID == "" || version == "" || doc.MetaData["document_id"] != documentID || doc.MetaData["version"] != version {
			return nil, ErrInvalidDocument
		}
	}
	ids := make([]string, 0, len(docs))
	for start := 0; start < len(docs); start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(ErrRetryable, err)
		}
		batch := docs[start:min(start+batchSize, len(docs))]
		written, err := store.Store(ctx, batch)
		if err != nil {
			return nil, errors.Join(ErrRetryable, err)
		}
		if len(written) != len(batch) {
			return nil, errors.Join(ErrRetryable, errors.New("store returned an incomplete batch"))
		}
		for i, id := range written {
			if id != batch[i].ID {
				return nil, errors.Join(ErrRetryable, errors.New("store returned unexpected document IDs"))
			}
		}
		ids = append(ids, written...)
	}
	// Only remove old versions after every new chunk has been acknowledged.
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrRetryable, err)
	}
	if err := store.DeleteOld(ctx, documentID, version); err != nil {
		return nil, errors.Join(ErrRetryable, err)
	}
	return ids, nil
}
