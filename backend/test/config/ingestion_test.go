package config_test

import (
	"path/filepath"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func knowledgeEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ONCALL_FILE_DIR", "ONCALL_UPLOAD_MAX_BYTES", "ONCALL_KNOWLEDGE_CHUNK_SIZE", "ONCALL_KNOWLEDGE_CHUNK_OVERLAP", "ONCALL_KNOWLEDGE_BATCH_SIZE"} {
		unsetEnv(t, key)
	}
}
func TestIngestionConfiguration(t *testing.T) {
	knowledgeEnvironment(t)
	c, err := config.LoadKnowledge()
	if err != nil || !filepath.IsAbs(c.FileDir) || c.MaxUploadBytes != 10485760 || c.ChunkSize != 2000 || c.ChunkOverlap != 200 || c.BatchSize != 16 {
		t.Fatalf("unexpected defaults: %v", err)
	}
	t.Setenv("ONCALL_KNOWLEDGE_CHUNK_SIZE", "65535")
	t.Setenv("ONCALL_KNOWLEDGE_CHUNK_OVERLAP", "0")
	t.Setenv("ONCALL_KNOWLEDGE_BATCH_SIZE", "1")
	c, err = config.LoadKnowledge()
	if err != nil || c.ChunkSize != 65535 || c.ChunkOverlap != 0 || c.BatchSize != 1 {
		t.Fatalf("overrides failed: %v", err)
	}
}
func TestInvalidIngestionConfiguration(t *testing.T) {
	for _, tt := range []struct{ key, value string }{
		{"ONCALL_FILE_DIR", ""}, {"ONCALL_UPLOAD_MAX_BYTES", ""}, {"ONCALL_UPLOAD_MAX_BYTES", "0"}, {"ONCALL_UPLOAD_MAX_BYTES", "9223372036854775807"},
		{"ONCALL_KNOWLEDGE_CHUNK_SIZE", "3"}, {"ONCALL_KNOWLEDGE_CHUNK_SIZE", "65536"},
		{"ONCALL_KNOWLEDGE_CHUNK_OVERLAP", "-1"}, {"ONCALL_KNOWLEDGE_CHUNK_OVERLAP", "2000"},
		{"ONCALL_KNOWLEDGE_BATCH_SIZE", "0"}, {"ONCALL_KNOWLEDGE_BATCH_SIZE", "bad"},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			knowledgeEnvironment(t)
			t.Setenv(tt.key, tt.value)
			if _, err := config.LoadKnowledge(); err == nil {
				t.Fatal("invalid ingestion configuration accepted")
			}
		})
	}
}
