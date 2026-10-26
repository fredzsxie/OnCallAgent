package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func TestKnowledgeDefaultsAndEnvironment(t *testing.T) {
	setEnvironment(t)
	c, err := config.LoadMilvus()
	if err != nil || c.Address != "localhost:19530" || c.Database != "default" || c.Collection != "oncall_knowledge" || c.TLS || c.ConnectTimeout != 5*time.Second {
		t.Fatalf("unexpected defaults: %v", err)
	}
	t.Setenv("ONCALL_MILVUS_ADDRESS", "127.0.0.1:19531")
	t.Setenv("ONCALL_MILVUS_TLS", "true")
	t.Setenv("ONCALL_MILVUS_USERNAME", "user")
	t.Setenv("ONCALL_MILVUS_PASSWORD", "password")
	c, err = config.LoadMilvus()
	if err != nil || c.Address != "127.0.0.1:19531" || !c.TLS || c.Username != "user" || c.Password != "password" {
		t.Fatalf("environment settings failed: %v", err)
	}
	e, err := config.LoadEmbedding()
	if err != nil || e.APIKey != "embedding-key" || e.Timeout != 30*time.Second {
		t.Fatalf("embedding defaults failed: %v", err)
	}
	t.Setenv("ONCALL_EMBEDDING_API_KEY", "explicit-key")
	e, err = config.LoadEmbedding()
	if err != nil || e.APIKey != "explicit-key" {
		t.Fatal("explicit key must take precedence")
	}
	t.Setenv("ONCALL_EMBEDDING_API_KEY", "")
	if _, err = config.LoadEmbedding(); err == nil {
		t.Fatal("explicit empty key must not fall back to ARK_API_KEY")
	}
}

func TestKnowledgeInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct {
		key, value string
		embedding  bool
	}{
		{"ONCALL_MILVUS_ADDRESS", "", false}, {"ONCALL_MILVUS_ADDRESS", "https://secret@host:19530", false},
		{"ONCALL_MILVUS_ADDRESS", "host:65536", false}, {"ONCALL_MILVUS_DATABASE", "", false},
		{"ONCALL_MILVUS_COLLECTION", "", false}, {"ONCALL_MILVUS_TLS", "invalid", false},
		{"ONCALL_MILVUS_CONNECT_TIMEOUT", "0s", false}, {"ONCALL_MILVUS_TIMEOUT", "-1s", false},
		{"ONCALL_MILVUS_USERNAME", "user", false}, {"ONCALL_MILVUS_PASSWORD", "password", false},
		{"ONCALL_EMBEDDING_BASE_URL", "https://secret@host/v3", true},
		{"ONCALL_EMBEDDING_BASE_URL", "https://example.invalid/api/v3/embeddings/multimodal", true},
		{"ONCALL_EMBEDDING_MODEL", "", true}, {"ONCALL_EMBEDDING_TIMEOUT", "0s", true},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			setEnvironment(t)
			t.Setenv(tt.key, tt.value)
			var err error
			if tt.embedding {
				_, err = config.LoadEmbedding()
			} else {
				_, err = config.LoadMilvus()
			}
			if err == nil || strings.Contains(err.Error(), "secret@") {
				t.Fatalf("expected safe validation failure: %v", err)
			}
		})
	}
}
