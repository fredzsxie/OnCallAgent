package config_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func knowledgeConfig(t *testing.T, content string) {
	t.Helper()
	adapter, err := gcfg.NewAdapterContent(content)
	if err != nil {
		t.Fatal(err)
	}
	old := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	t.Cleanup(func() { g.Cfg().SetAdapter(old) })
	for _, key := range []string{"ONCALL_EMBEDDING_BASE_URL", "ONCALL_EMBEDDING_MODEL", "ONCALL_EMBEDDING_API_KEY", "ONCALL_EMBEDDING_TIMEOUT", "ONCALL_EMBEDDING_DIMENSIONS", "ONCALL_MILVUS_ADDRESS", "ONCALL_MILVUS_DATABASE", "ONCALL_MILVUS_COLLECTION", "ONCALL_MILVUS_TLS", "ONCALL_MILVUS_CA_FILE", "ONCALL_MILVUS_SERVER_NAME", "ONCALL_MILVUS_USERNAME", "ONCALL_MILVUS_PASSWORD", "ONCALL_MILVUS_TOKEN", "ONCALL_MILVUS_TIMEOUT", "ONCALL_MILVUS_CONNECT_TIMEOUT"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ARK_API_KEY", "fixture-key")
}

func TestKnowledgeDefaultsAndOverrides(t *testing.T) {
	knowledgeConfig(t, "milvus:\n  address: example.invalid:19530\nembedding:\n  timeout: 2s\n")
	ctx := context.Background()
	c, err := config.LoadMilvus(ctx)
	if err != nil || c.Address != "example.invalid:19530" || c.Database != "default" || c.TLS || c.ConnectTimeout != 5*time.Second {
		t.Fatalf("unexpected Milvus config: %v", err)
	}
	t.Setenv("ONCALL_MILVUS_ADDRESS", "127.0.0.1:19531")
	t.Setenv("ONCALL_MILVUS_TLS", "true")
	t.Setenv("ONCALL_MILVUS_TOKEN", "fixture-token")
	c, err = config.LoadMilvus(ctx)
	if err != nil || c.Address != "127.0.0.1:19531" || !c.TLS || c.Token != "fixture-token" {
		t.Fatalf("override failed: %v", err)
	}
	e, err := config.LoadEmbedding(ctx)
	if err != nil || e.APIKey != "fixture-key" || e.Timeout != 2*time.Second || e.Dimensions != 0 {
		t.Fatalf("embedding defaults failed: %v", err)
	}
	t.Setenv("ONCALL_EMBEDDING_API_KEY", "explicit-key")
	e, err = config.LoadEmbedding(ctx)
	if err != nil || e.APIKey != "explicit-key" {
		t.Fatal("explicit embedding key must take precedence")
	}
	t.Setenv("ONCALL_EMBEDDING_API_KEY", "")
	if _, err = config.LoadEmbedding(ctx); err == nil {
		t.Fatal("explicit empty key must not fall back to ARK_API_KEY")
	}
}

func TestKnowledgeInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct {
		key, value string
		embedding  bool
	}{
		{"ONCALL_MILVUS_ADDRESS", "", false}, {"ONCALL_MILVUS_ADDRESS", "https://secret@host:19530", false},
		{"ONCALL_MILVUS_ADDRESS", "host:65536", false}, {"ONCALL_MILVUS_DATABASE", "bad-name", false},
		{"ONCALL_MILVUS_COLLECTION", "", false}, {"ONCALL_MILVUS_TLS", "invalid", false},
		{"ONCALL_MILVUS_CONNECT_TIMEOUT", "0s", false}, {"ONCALL_MILVUS_TIMEOUT", "-1s", false},
		{"ONCALL_MILVUS_USERNAME", "user", false}, {"ONCALL_MILVUS_CA_FILE", "ca.pem", false},
		{"ONCALL_EMBEDDING_BASE_URL", "https://secret@host/v3", true}, {"ONCALL_EMBEDDING_MODEL", "", true},
		{"ONCALL_EMBEDDING_BASE_URL", "https://example.invalid/api/v3/embeddings/multimodal", true},
		{"ONCALL_EMBEDDING_TIMEOUT", "0s", true}, {"ONCALL_EMBEDDING_DIMENSIONS", "wrong", true},
		{"ONCALL_EMBEDDING_DIMENSIONS", "-1", true}, {"ONCALL_EMBEDDING_DIMENSIONS", "32769", true},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			knowledgeConfig(t, "server:\n  port: 6872\n")
			t.Setenv(tt.key, tt.value)
			var err error
			if tt.embedding {
				_, err = config.LoadEmbedding(context.Background())
			} else {
				_, err = config.LoadMilvus(context.Background())
			}
			if err == nil || strings.Contains(err.Error(), "secret@") {
				t.Fatalf("expected safe validation failure: %v", err)
			}
		})
	}
}

func TestRejectKnowledgeSecretsInYAML(t *testing.T) {
	for _, field := range []string{"embedding.api_key", "milvus.username", "milvus.password", "milvus.token"} {
		t.Run(field, func(t *testing.T) {
			parts := strings.Split(field, ".")
			knowledgeConfig(t, parts[0]+":\n  "+parts[1]+": hidden-secret\n")
			var err error
			if parts[0] == "embedding" {
				_, err = config.LoadEmbedding(context.Background())
			} else {
				_, err = config.LoadMilvus(context.Background())
			}
			if err == nil || strings.Contains(err.Error(), "hidden-secret") {
				t.Fatalf("expected YAML secret rejection: %v", err)
			}
		})
	}
}
