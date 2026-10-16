package knowledge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

var (
	ErrInvalidDocument   = errors.New("invalid document")
	ErrTooLarge          = errors.New("file is too large")
	ErrUnsupportedFormat = errors.New("unsupported file format")
	ErrRetryable         = errors.New("knowledge ingestion failed; retry the upload")
)

type DocumentStore interface {
	Store(context.Context, []*schema.Document, ...indexer.Option) ([]string, error)
	DeleteOld(context.Context, string, string) error
}

type Result struct {
	FileName    string `json:"fileName"`
	FilePath    string `json:"filePath"`
	FileSize    int64  `json:"fileSize"`
	Source      string `json:"source"`
	ContentHash string `json:"contentHash"`
	ChunkCount  int    `json:"chunkCount"`
}

type KnowledgeService struct {
	cfg   config.Knowledge
	store DocumentStore
	locks sync.Map
}

func NewKnowledgeService(cfg config.Knowledge, store DocumentStore) (*KnowledgeService, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("document store is required")
	}
	return &KnowledgeService{cfg: cfg, store: store}, nil
}

func Supported(source string) bool {
	switch strings.ToLower(path.Ext(source)) {
	case ".txt", ".md", ".markdown":
		return true
	}
	return false
}

func UploadName(filename string) string { return path.Base(strings.ReplaceAll(filename, "\\", "/")) }

func (s *KnowledgeService) Ingest(ctx context.Context, source string, input io.Reader) (Result, error) {
	if input == nil {
		return Result{}, ErrInvalidDocument
	}
	source = strings.ReplaceAll(source, "\\", "/")
	if !utf8.ValidString(source) || len(source) > 4096 || source == "" || path.IsAbs(source) || strings.ContainsAny(source, "\x00\r\n") {
		return Result{}, ErrInvalidDocument
	}
	for _, part := range strings.Split(source, "/") {
		if part == ".." {
			return Result{}, ErrInvalidDocument
		}
	}
	source = path.Clean(source)
	if !Supported(source) {
		return Result{}, ErrUnsupportedFormat
	}
	// Serialise each source within this process so updates cannot delete each other's chunks.
	value, _ := s.locks.LoadOrStore(digest(source), make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return Result{}, errors.Join(ErrRetryable, ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return Result{}, errors.Join(ErrRetryable, err)
	}
	data, err := io.ReadAll(io.LimitReader(input, s.cfg.MaxUploadBytes+1))
	if err != nil {
		return Result{}, errors.Join(ErrRetryable, err)
	}
	if int64(len(data)) > s.cfg.MaxUploadBytes {
		return Result{}, ErrTooLarge
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return Result{}, ErrInvalidDocument
	}
	hash := digest(string(data))
	docs, err := Split(source, string(data), hash, s.cfg)
	if err != nil {
		return Result{}, err
	}
	savedPath, size, err := s.save(digest(source)+"_"+hash+strings.ToLower(path.Ext(source)), data)
	if err != nil {
		return Result{}, errors.Join(ErrRetryable, err)
	}
	result := Result{filepath.Base(savedPath), savedPath, size, source, hash, len(docs)}
	for start := 0; start < len(docs); start += s.cfg.BatchSize {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(ErrRetryable, err)
		}
		batch := docs[start:min(start+s.cfg.BatchSize, len(docs))]
		ids, err := s.store.Store(ctx, batch)
		if err != nil {
			return result, errors.Join(ErrRetryable, err)
		}
		if len(ids) != len(batch) {
			return result, errors.Join(ErrRetryable, errors.New("store returned an incomplete batch"))
		}
		for i, id := range ids {
			if id != batch[i].ID {
				return result, errors.Join(ErrRetryable, errors.New("store returned unexpected document IDs"))
			}
		}
	}
	// Keep every old version until all new chunks have been acknowledged.
	if err := ctx.Err(); err != nil {
		return result, errors.Join(ErrRetryable, err)
	}
	if err := s.store.DeleteOld(ctx, docs[0].MetaData["document_id"].(string), docs[0].MetaData["version"].(string)); err != nil {
		return result, errors.Join(ErrRetryable, err)
	}
	return result, nil
}

func (s *KnowledgeService) IngestFile(ctx context.Context, source, filename string) (result Result, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return result, errors.Join(ErrRetryable, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, ErrRetryable, closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return result, errors.Join(ErrRetryable, err)
	}
	if !info.Mode().IsRegular() {
		return result, ErrInvalidDocument
	}
	return s.Ingest(ctx, source, file)
}

func (s *KnowledgeService) save(name string, data []byte) (saved string, size int64, err error) {
	if err = os.MkdirAll(s.cfg.FileDir, 0700); err != nil {
		return
	}
	root, rootErr := filepath.EvalSymlinks(s.cfg.FileDir)
	if rootErr != nil {
		return "", 0, rootErr
	}
	file, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		return "", 0, err
	}
	temporary := file.Name()
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	n, err := file.Write(data)
	if err != nil {
		return "", 0, err
	}
	if n != len(data) {
		return "", 0, io.ErrShortWrite
	}
	if err = file.Sync(); err != nil {
		return "", 0, err
	}
	closeErr := file.Close()
	file = nil
	if closeErr != nil {
		return "", 0, closeErr
	}
	saved = filepath.Join(root, name)
	if err = os.Rename(temporary, saved); err != nil {
		return "", 0, err
	}
	info, err := os.Stat(saved)
	if err != nil {
		return "", 0, err
	}
	if info.Size() != int64(len(data)) {
		return "", 0, fmt.Errorf("saved file size does not match input")
	}
	return saved, info.Size(), nil
}
