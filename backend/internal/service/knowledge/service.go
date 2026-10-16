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
	var err error
	source, err = NormalizeSource(source)
	if err != nil {
		return Result{}, err
	}
	unlock, err := LockSource(ctx, source)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	data, err := ReadDocument(ctx, input, s.cfg.MaxUploadBytes)
	if err != nil {
		return Result{}, err
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
	_, err = IndexChunks(ctx, s.store, docs, s.cfg.BatchSize)
	return result, err
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
