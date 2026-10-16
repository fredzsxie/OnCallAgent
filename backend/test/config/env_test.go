package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func changeDir(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Error(err)
		}
	})
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	// Record the original value for cleanup, then simulate an absent process variable.
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func writeEnv(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEnvFromBackendOrRepositoryRoot(t *testing.T) {
	for _, path := range []string{".env", "backend/.env"} {
		t.Run(path, func(t *testing.T) {
			changeDir(t, t.TempDir())
			unsetEnv(t, "ONCALL_DOTENV_TEST")
			writeEnv(t, path, "export ONCALL_DOTENV_TEST=\"value with # and =\"\n")
			if err := config.LoadEnv(); err != nil {
				t.Fatal(err)
			}
			if os.Getenv("ONCALL_DOTENV_TEST") != "value with # and =" {
				t.Fatal(".env was not loaded or quoted content was changed")
			}
		})
	}
}

func TestLoadEnvPreservesProcessVariablesIncludingEmptyValues(t *testing.T) {
	changeDir(t, t.TempDir())
	t.Setenv("ONCALL_DOTENV_KEEP", "exported")
	t.Setenv("ONCALL_DOTENV_EMPTY", "")
	writeEnv(t, ".env", "ONCALL_DOTENV_KEEP=from-file\nONCALL_DOTENV_EMPTY=from-file\n")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ONCALL_DOTENV_KEEP") != "exported" || os.Getenv("ONCALL_DOTENV_EMPTY") != "" {
		t.Fatal(".env overwrote a process environment variable")
	}
}

func TestLoadEnvUsesOnlyFirstExistingFile(t *testing.T) {
	changeDir(t, t.TempDir())
	unsetEnv(t, "ONCALL_DOTENV_TEST")
	unsetEnv(t, "ONCALL_DOTENV_SECOND")
	writeEnv(t, ".env", "ONCALL_DOTENV_TEST=first\n")
	writeEnv(t, "backend/.env", "ONCALL_DOTENV_TEST=second\nONCALL_DOTENV_SECOND=must-not-load\n")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ONCALL_DOTENV_TEST") != "first" {
		t.Fatal("working-directory .env was not preferred")
	}
	if _, exists := os.LookupEnv("ONCALL_DOTENV_SECOND"); exists {
		t.Fatal("second .env was unexpectedly merged")
	}
}

func TestMissingEnvFileIsOptional(t *testing.T) {
	changeDir(t, t.TempDir())
	if err := config.LoadEnv(); err != nil {
		t.Fatalf("environment-only deployment should work without .env: %v", err)
	}
}

func TestInvalidEnvFileDoesNotLeakContent(t *testing.T) {
	changeDir(t, t.TempDir())
	writeEnv(t, ".env", "BAD?KEY=test-secret\n")
	err := config.LoadEnv()
	if err == nil || !strings.Contains(err.Error(), "KEY=value") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("expected safe .env syntax error, got %v", err)
	}
}

func TestEnvPathMustBeFile(t *testing.T) {
	changeDir(t, t.TempDir())
	if err := os.Mkdir(".env", 0700); err != nil {
		t.Fatal(err)
	}
	if err := config.LoadEnv(); err == nil {
		t.Fatal("directory named .env should be rejected")
	}
}

func TestDotEnvProvidesChatModelConfiguration(t *testing.T) {
	setEnvironment(t)
	changeDir(t, t.TempDir())
	for _, key := range []string{"ONCALL_CHAT_MODEL_BASE_URL", "ONCALL_CHAT_MODEL_MODEL", "ONCALL_CHAT_MODEL_API_KEY"} {
		unsetEnv(t, key)
	}
	writeEnv(t, ".env", "ONCALL_CHAT_MODEL_BASE_URL=https://env.example/v1\nONCALL_CHAT_MODEL_MODEL=env-model\nONCALL_CHAT_MODEL_API_KEY=\"test-key#with=characters\"\n")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadChatModel()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://env.example/v1" || cfg.Model != "env-model" || cfg.APIKey != "test-key#with=characters" {
		t.Fatal(".env model settings or API key parsing failed")
	}
}
