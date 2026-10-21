package config_test

import (
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"path/filepath"
	"testing"
)

func TestLoggingDefaultsAndEnvPriority(t *testing.T) {
	for _, key := range []string{"ONCALL_LOG_LEVEL", "ONCALL_LOG_FILE_ENABLED", "ONCALL_LOG_DIR", "ONCALL_LOG_RETENTION_DAYS"} {
		unsetEnv(t, key)
	}
	c, err := config.LoadLogging()
	if err != nil || c.Level != "info" || !c.FileEnabled || c.Dir != "./log" || c.RetentionDays != 7 {
		t.Fatal(c, err)
	}
	dir := t.TempDir()
	changeDir(t, dir)
	writeEnv(t, filepath.Join(dir, ".env"), "ONCALL_LOG_LEVEL=debug\nONCALL_LOG_RETENTION_DAYS=14\n")
	t.Setenv("ONCALL_LOG_LEVEL", "error")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	c, err = config.LoadLogging()
	if err != nil || c.Level != "error" || c.RetentionDays != 14 {
		t.Fatal("priority", c, err)
	}
	for _, key := range []string{"ONCALL_LOG_LEVEL", "ONCALL_LOG_FILE_ENABLED", "ONCALL_LOG_DIR", "ONCALL_LOG_RETENTION_DAYS"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			if _, err := config.LoadLogging(); err == nil {
				t.Fatal("accepted explicit empty", key)
			}
		})
	}
	for _, value := range []string{"0", "-1", "not-number", "36501"} {
		t.Setenv("ONCALL_LOG_RETENTION_DAYS", value)
		if _, err := config.LoadLogging(); err == nil {
			t.Fatal("accepted retention", value)
		}
	}
}
