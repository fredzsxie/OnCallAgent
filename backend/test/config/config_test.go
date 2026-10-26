package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func setEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"ONCALL_SERVER_PORT", "ONCALL_CHAT_MODEL_BASE_URL", "ONCALL_CHAT_MODEL_MODEL", "ONCALL_CHAT_MODEL_API_KEY", "ONCALL_CHAT_MODEL_TIMEOUT",
		"ONCALL_EMBEDDING_BASE_URL", "ONCALL_EMBEDDING_MODEL", "ONCALL_EMBEDDING_API_KEY", "ONCALL_EMBEDDING_TIMEOUT",
		"ONCALL_MILVUS_ADDRESS", "ONCALL_MILVUS_DATABASE", "ONCALL_MILVUS_COLLECTION", "ONCALL_MILVUS_USERNAME", "ONCALL_MILVUS_PASSWORD", "ONCALL_MILVUS_TLS", "ONCALL_MILVUS_CONNECT_TIMEOUT", "ONCALL_MILVUS_TIMEOUT",
	} {
		unsetEnv(t, name)
	}
	t.Setenv("ONCALL_CHAT_MODEL_BASE_URL", "https://example.invalid/v1")
	t.Setenv("ONCALL_CHAT_MODEL_MODEL", "test-model")
	t.Setenv("ONCALL_CHAT_MODEL_API_KEY", "test-key")
	t.Setenv("ARK_API_KEY", "embedding-key")
}

func TestDefaultAndEnvironmentPort(t *testing.T) {
	setEnvironment(t)
	cfg, err := config.Load()
	if err != nil || cfg.Port != 6872 {
		t.Fatalf("default port failed: %v", err)
	}
	t.Setenv("ONCALL_SERVER_PORT", "7888")
	cfg, err = config.Load()
	if err != nil || cfg.Port != 7888 {
		t.Fatalf("port override failed: %v", err)
	}
	for _, value := range []string{"", "abc", "0", "-1", "65536"} {
		t.Setenv("ONCALL_SERVER_PORT", value)
		if _, err := config.Load(); err == nil {
			t.Fatalf("invalid port accepted: %q", value)
		}
	}
}

func TestChatModelEnvironment(t *testing.T) {
	setEnvironment(t)
	t.Setenv("ONCALL_CHAT_MODEL_TIMEOUT", "750ms")
	cfg, err := config.LoadChatModel()
	if err != nil || cfg.Model != "test-model" || cfg.APIKey != "test-key" || cfg.Timeout != 750*time.Millisecond {
		t.Fatalf("model settings failed: %v", err)
	}
}

func TestInvalidChatModelConfiguration(t *testing.T) {
	for _, tt := range []struct{ env, value string }{
		{"ONCALL_CHAT_MODEL_BASE_URL", ""}, {"ONCALL_CHAT_MODEL_BASE_URL", "ftp://example.invalid"},
		{"ONCALL_CHAT_MODEL_BASE_URL", "https://user:test-secret@example.invalid/v1"},
		{"ONCALL_CHAT_MODEL_BASE_URL", "https://example.invalid/v1/chat/completions"},
		{"ONCALL_CHAT_MODEL_MODEL", " "}, {"ONCALL_CHAT_MODEL_API_KEY", ""},
		{"ONCALL_CHAT_MODEL_TIMEOUT", "invalid"}, {"ONCALL_CHAT_MODEL_TIMEOUT", "0s"},
	} {
		t.Run(tt.env+tt.value, func(t *testing.T) {
			setEnvironment(t)
			t.Setenv(tt.env, tt.value)
			_, err := config.LoadChatModel()
			if err == nil || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("expected safe validation error: %v", err)
			}
		})
	}
}
