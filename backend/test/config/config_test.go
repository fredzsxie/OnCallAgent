package config_test

import (
	"context"
	"os"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func setConfig(t *testing.T, content string) {
	t.Helper()
	adapter, err := gcfg.NewAdapterContent(content)
	if err != nil {
		t.Fatal(err)
	}
	original := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	t.Cleanup(func() { g.Cfg().SetAdapter(original) })
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
