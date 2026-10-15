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

func setConfig(t *testing.T, content string) {
	t.Helper()
	content += "\nchat_model:\n  base_url: https://example.invalid/v1\n  model: test-model\n"
	adapter, err := gcfg.NewAdapterContent(content)
	if err != nil {
		t.Fatal(err)
	}
	original := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	t.Cleanup(func() { g.Cfg().SetAdapter(original) })
	t.Setenv("ONCALL_CHAT_MODEL_API_KEY", "test-key")
	for _, name := range []string{"ONCALL_CHAT_MODEL_BASE_URL", "ONCALL_CHAT_MODEL_MODEL", "ONCALL_CHAT_MODEL_TIMEOUT"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDefaultAndYAMLPort(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		port    int
	}{
		{"default", "server:\n  address: ':6872'\n", 6872},
		{"YAML", "server:\n  port: 7000", 7000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setConfig(t, tt.content)
			// Setenv records the original value for cleanup; remove it for the YAML check.
			t.Setenv("ONCALL_SERVER_PORT", "")
			if err := os.Unsetenv("ONCALL_SERVER_PORT"); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(context.Background())
			if err != nil || cfg.Port != tt.port {
				t.Fatalf("want port %d, got %d, error: %v", tt.port, cfg.Port, err)
			}
		})
	}
}

func TestEnvironmentOverridesYAML(t *testing.T) {
	setConfig(t, "server:\n  port: 7000")
	t.Setenv("ONCALL_SERVER_PORT", "7888")
	cfg, err := config.Load(context.Background())
	if err != nil || cfg.Port != 7888 {
		t.Fatalf("environment override failed: port=%d, error=%v", cfg.Port, err)
	}
}

func TestInvalidPort(t *testing.T) {
	setConfig(t, "server:\n  port: 6872")
	for _, value := range []string{"", "abc", "0", "-1", "65536"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ONCALL_SERVER_PORT", value)
			if _, err := config.Load(context.Background()); err == nil {
				t.Fatalf("expected invalid port %q to fail", value)
			}
		})
	}
}

func TestChatModelEnvironmentOverrides(t *testing.T) {
	setConfig(t, "server:\n  port: 6872")
	t.Setenv("ONCALL_CHAT_MODEL_BASE_URL", "http://127.0.0.1:18000/v1")
	t.Setenv("ONCALL_CHAT_MODEL_MODEL", "actual-model-id")
	t.Setenv("ONCALL_CHAT_MODEL_API_KEY", "another-test-key")
	t.Setenv("ONCALL_CHAT_MODEL_TIMEOUT", "750ms")
	cfg, err := config.LoadChatModel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "http://127.0.0.1:18000/v1" || cfg.Model != "actual-model-id" || cfg.APIKey != "another-test-key" || cfg.Timeout != 750*time.Millisecond {
		t.Fatal("model environment overrides did not apply")
	}
}

func TestInvalidChatModelConfiguration(t *testing.T) {
	for _, tt := range []struct{ env, value string }{
		{"ONCALL_CHAT_MODEL_BASE_URL", ""},
		{"ONCALL_CHAT_MODEL_BASE_URL", "ftp://example.invalid"},
		{"ONCALL_CHAT_MODEL_BASE_URL", "https://user:test-secret@example.invalid/v1"},
		{"ONCALL_CHAT_MODEL_BASE_URL", "https://example.invalid/v1/chat/completions"},
		{"ONCALL_CHAT_MODEL_MODEL", " "},
		{"ONCALL_CHAT_MODEL_API_KEY", ""},
		{"ONCALL_CHAT_MODEL_TIMEOUT", "invalid"},
		{"ONCALL_CHAT_MODEL_TIMEOUT", "0s"},
	} {
		t.Run(tt.env+"/"+tt.value, func(t *testing.T) {
			setConfig(t, "server:\n  port: 6872")
			t.Setenv(tt.env, tt.value)
			_, err := config.LoadChatModel(context.Background())
			if err == nil || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("expected safe validation error, got %v", err)
			}
		})
	}
}

func TestRejectAPIKeyInYAML(t *testing.T) {
	setConfig(t, "server:\n  port: 6872")
	adapter, err := gcfg.NewAdapterContent("chat_model:\n  base_url: https://example.invalid/v1\n  model: test-model\n  api_key: test-secret\n")
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	_, err = config.LoadChatModel(context.Background())
	if err == nil || !strings.Contains(err.Error(), "must not be configured in YAML") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("expected safe key-in-YAML error, got %v", err)
	}
}
