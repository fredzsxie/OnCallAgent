package config

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
)

type Knowledge struct {
	CatalogPath    string
	FileDir        string
	MaxUploadBytes int64
	ChunkSize      int
	ChunkOverlap   int
	BatchSize      int
}

func LoadKnowledge() (Knowledge, error) {
	c := Knowledge{}
	var err error
	if c.FileDir = env("ONCALL_FILE_DIR", "./data/knowledge"); c.FileDir == "" {
		return c, fmt.Errorf("ONCALL_FILE_DIR is required")
	}
	if c.FileDir, err = filepath.Abs(c.FileDir); err != nil {
		return c, fmt.Errorf("cannot resolve ONCALL_FILE_DIR")
	}
	c.CatalogPath = env("ONCALL_KNOWLEDGE_CATALOG_PATH", filepath.Join(c.FileDir, "catalog.sqlite"))
	if c.CatalogPath == "" {
		return c, fmt.Errorf("ONCALL_KNOWLEDGE_CATALOG_PATH must not be empty")
	}
	if c.CatalogPath, err = filepath.Abs(c.CatalogPath); err != nil {
		return c, err
	}
	if c.MaxUploadBytes, err = strconv.ParseInt(env("ONCALL_UPLOAD_MAX_BYTES", "10485760"), 10, 64); err != nil {
		return c, fmt.Errorf("ONCALL_UPLOAD_MAX_BYTES must be a positive integer")
	}
	for _, field := range []struct {
		name, fallback string
		target         *int
	}{
		{"ONCALL_KNOWLEDGE_CHUNK_SIZE", "2000", &c.ChunkSize},
		{"ONCALL_KNOWLEDGE_CHUNK_OVERLAP", "200", &c.ChunkOverlap},
		{"ONCALL_KNOWLEDGE_BATCH_SIZE", "16", &c.BatchSize},
	} {
		if *field.target, err = strconv.Atoi(env(field.name, field.fallback)); err != nil {
			return c, fmt.Errorf("%s must be an integer", field.name)
		}
	}
	return c, c.Validate()
}

func (c Knowledge) Validate() error {
	if c.FileDir == "" || !filepath.IsAbs(c.FileDir) {
		return fmt.Errorf("file directory must be absolute")
	}
	if c.MaxUploadBytes <= 0 || c.MaxUploadBytes > math.MaxInt64-(1<<20)-1 {
		return fmt.Errorf("upload byte limit must be positive and leave room for multipart overhead")
	}
	if c.ChunkSize < 4 || c.ChunkSize > 65535 {
		return fmt.Errorf("chunk size must be between 4 and 65535 UTF-8 bytes")
	}
	if c.ChunkOverlap < 0 || c.ChunkOverlap >= c.ChunkSize {
		return fmt.Errorf("chunk overlap must be nonnegative and smaller than chunk size")
	}
	if c.BatchSize < 1 {
		return fmt.Errorf("batch size must be positive")
	}
	return nil
}
