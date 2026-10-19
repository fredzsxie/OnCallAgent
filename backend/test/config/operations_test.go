package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func TestOperationsDefaultsAndEnvPriority(t *testing.T) {
	for _, name := range []string{"ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED", "ONCALL_TOOLS_QUERY_LOG_ENABLED", "ONCALL_PROMETHEUS_BASE_URL", "ONCALL_PROMETHEUS_TIMEOUT", "ONCALL_PROMETHEUS_AUTH_TYPE"} {
		unsetEnv(t, name)
	}
	tools, err := config.LoadTools()
	if err != nil || tools.QueryLogEnabled || tools.QueryPrometheusAlertsEnabled {
		t.Fatal("operations must default to disabled", err)
	}
	p, err := config.LoadPrometheus()
	if err != nil || p.URL != "http://127.0.0.1:9090" || p.Timeout != 15*time.Second || p.Auth.Type != "none" {
		t.Fatalf("defaults %+v %v", p, err)
	}
	changeDir(t, t.TempDir())
	writeEnv(t, ".env", "ONCALL_PROMETHEUS_BASE_URL=http://localhost:9999\nONCALL_PROMETHEUS_TIMEOUT=7s\n")
	t.Setenv("ONCALL_PROMETHEUS_TIMEOUT", "3s")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	p, err = config.LoadPrometheus()
	if err != nil || p.URL != "http://localhost:9999" || p.Timeout != 3*time.Second {
		t.Fatal("priority not respected", err)
	}
}

func TestOperationsConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		log         bool
	}{
		{"ONCALL_PROMETHEUS_TIMEOUT", "", false}, {"ONCALL_PROMETHEUS_TIMEOUT", "0s", false},
		{"ONCALL_PROMETHEUS_BASE_URL", "", false}, {"ONCALL_PROMETHEUS_BASE_URL", "http://user:private@host", false},
		{"ONCALL_PROMETHEUS_BASE_URL", "https://host?token=private", false}, {"ONCALL_PROMETHEUS_AUTH_TYPE", "header", false},
		{"ONCALL_LOG_LOOKBACK", "", true}, {"ONCALL_LOG_LOOKBACK", "0s", true}, {"ONCALL_LOG_LOOKBACK", "1ns", true},
		{"ONCALL_LOG_LIMIT", "", true}, {"ONCALL_LOG_LIMIT", "0", true}, {"ONCALL_LOG_LIMIT", "1001", true},
		{"ONCALL_LOG_MCP_URL", "", true}, {"ONCALL_LOG_MCP_TIMEOUT", "bad", true},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			t.Setenv("ONCALL_LOG_MCP_URL", "http://localhost:3000/mcp")
			t.Setenv(tc.name, tc.value)
			var err error
			if tc.log {
				_, err = config.LoadLogMCP()
			} else {
				_, err = config.LoadPrometheus()
			}
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("invalid or unsafe config error: %v", err)
			}
		})
	}
	for _, name := range []string{"ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED", "ONCALL_TOOLS_QUERY_LOG_ENABLED"} {
		for _, value := range []string{"", "yes"} {
			t.Run(name+value, func(t *testing.T) {
				t.Setenv(name, value)
				if _, err := config.LoadTools(); err == nil {
					t.Fatal("invalid enabled accepted")
				}
			})
		}
	}
}

func TestOperationsAuthenticationValidation(t *testing.T) {
	for _, auth := range []config.HTTPAuth{
		{Type: "none"}, {Type: "basic", Username: "reader", Password: "fixture-only"}, {Type: "bearer", BearerToken: "fixture-only"}, {Type: "header", HeaderName: "X-API-Key", HeaderValue: "fixture-only"},
	} {
		if err := auth.Validate(true); err != nil {
			t.Fatal(err)
		}
	}
	for _, auth := range []config.HTTPAuth{
		{Type: "basic", Username: "reader"}, {Type: "bearer", BearerToken: " "}, {Type: "none", Password: "private"},
		{Type: "basic", Username: "reader", Password: "private", BearerToken: "private"},
		{Type: "header", HeaderName: "Accept", HeaderValue: "private"}, {Type: "header", HeaderName: "Mcp-Session-Id", HeaderValue: "private"},
		{Type: "header", HeaderName: "invalid header", HeaderValue: "private"}, {Type: "header", HeaderName: "X-Key", HeaderValue: "private\nvalue"},
	} {
		if err := auth.Validate(true); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid auth accepted or leaked", err)
		}
	}
}
