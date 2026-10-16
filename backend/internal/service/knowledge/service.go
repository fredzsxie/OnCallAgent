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

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

var (
	ErrInvalidDocument   = errors.New("invalid document")
	ErrTooLarge          = errors.New("file is too large")
	ErrUnsupportedFormat = errors.New("unsupported file format")
	ErrRetryable         = errors.New("knowledge ingestion failed; retry the upload")
	ErrSave              = errors.New("file save failed")
	ErrIngest            = errors.New("knowledge graph ingestion failed")
)

type IndexFileFunc func(context.Context, document.Source, string) ([]string, error)

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
	cfg       config.Knowledge
	indexFile IndexFileFunc
}

func NewKnowledgeService(cfg config.Knowledge, indexFile IndexFileFunc) (*KnowledgeService, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if indexFile == nil {
		return nil, errors.New("file indexer is required")
	}
	return &KnowledgeService{cfg: cfg, indexFile: indexFile}, nil
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
	// Uploads lock the entire save/invoke operation; the graph uses a separate write lock.
	unlock, err := lockSource(ctx, source, &uploadLocks)
	if err != nil {
		return Result{}, errors.Join(ErrIngest, err)
	}
	defer unlock()
	data, err := ReadDocument(ctx, input, s.cfg.MaxUploadBytes)
	if err != nil {
		if errors.Is(err, ErrRetryable) {
			return Result{}, errors.Join(ErrSave, err)
		}
		return Result{}, err
	}
	hash := digest(string(data))
	savedPath, size, err := s.save(digest(source)+"_"+hash+strings.ToLower(path.Ext(source)), data)
	if err != nil {
		return Result{}, errors.Join(ErrSave, ErrRetryable, err)
	}
	result := Result{FileName: filepath.Base(savedPath), FilePath: savedPath, FileSize: size, Source: source, ContentHash: hash}
	ids, err := s.indexFile(ctx, document.Source{URI: savedPath}, source)
	if err != nil {
		return result, errors.Join(ErrIngest, ErrRetryable, err)
	}
	if len(ids) == 0 {
		return result, errors.Join(ErrIngest, ErrRetryable, errors.New("graph returned no chunk IDs"))
	}
	result.ChunkCount = len(ids)
	return result, nil
}

func (s *KnowledgeService) IngestFile(ctx context.Context, source, filename string) (result Result, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return result, errors.Join(ErrSave, ErrRetryable, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, ErrSave, ErrRetryable, closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return result, errors.Join(ErrSave, ErrRetryable, err)
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
