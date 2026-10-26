package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func TestOpsDefaultsLimitsAndModelInheritance(t *testing.T) {
	keys := []string{"ONCALL_OPS_MAX_ITERATIONS", "ONCALL_OPS_EXECUTOR_MAX_STEPS", "ONCALL_OPS_TIMEOUT"}
	for _, prefix := range []string{"ONCALL_OPS_PLANNER", "ONCALL_OPS_EXECUTOR"} {
		for _, suffix := range []string{"_BASE_URL", "_MODEL", "_API_KEY", "_TIMEOUT"} {
			keys = append(keys, prefix+suffix)
		}
	}
	for _, key := range keys {
		old, ok := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if ok {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	cfg, err := config.LoadOpsAgent()
	if err != nil || cfg.MaxIterations != 8 || cfg.ExecutorMaxSteps != 12 || cfg.Timeout != 180*time.Second {
		t.Fatal(cfg, err)
	}
	for key, value := range map[string]string{"ONCALL_CHAT_MODEL_BASE_URL": "https://example.test/v1", "ONCALL_CHAT_MODEL_MODEL": "chat", "ONCALL_CHAT_MODEL_API_KEY": "test-key", "ONCALL_CHAT_MODEL_TIMEOUT": "5s"} {
		t.Setenv(key, value)
	}
	p, e, err := config.LoadOpsModels()
	if err != nil || p != e || p.Model != "chat" || p.Timeout != 5*time.Second {
		t.Fatal("inheritance", err)
	}
	t.Setenv("ONCALL_OPS_PLANNER_MODEL", "planner")
	t.Setenv("ONCALL_OPS_EXECUTOR_TIMEOUT", "7s")
	p, e, err = config.LoadOpsModels()
	if err != nil || p.Model != "planner" || e.Model != "chat" || e.Timeout != 7*time.Second {
		t.Fatal("override", err)
	}
	t.Setenv("ONCALL_OPS_PLANNER_API_KEY", "")
	if _, _, err = config.LoadOpsModels(); err == nil || strings.Contains(err.Error(), "test-key") {
		t.Fatal("empty key accepted or leaked")
	}
}

func TestOpsInvalidConfigAndIndependentModels(t *testing.T) {
	for _, key := range []string{"ONCALL_OPS_MAX_ITERATIONS", "ONCALL_OPS_EXECUTOR_MAX_STEPS", "ONCALL_OPS_TIMEOUT"} {
		for _, value := range []string{"", "0", "-1", "bad"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := config.LoadOpsAgent(); err == nil {
					t.Fatal("accepted invalid value")
				}
			})
		}
	}
	for _, prefix := range []string{"ONCALL_CHAT_MODEL", "ONCALL_OPS_PLANNER", "ONCALL_OPS_EXECUTOR"} {
		for suffix, value := range map[string]string{"_BASE_URL": "https://example.test/v1", "_MODEL": "test", "_API_KEY": "test-key", "_TIMEOUT": "1s"} {
			if prefix == "ONCALL_CHAT_MODEL" {
				value = ""
			}
			t.Setenv(prefix+suffix, value)
		}
	}
	if _, _, err := config.LoadOpsModels(); err != nil {
		t.Fatal("dedicated models require no ordinary model", err)
	}
	for _, suffix := range []string{"BASE_URL", "MODEL", "API_KEY", "TIMEOUT"} {
		t.Run(suffix, func(t *testing.T) {
			t.Setenv("ONCALL_OPS_EXECUTOR_"+suffix, "")
			if _, _, err := config.LoadOpsModels(); err == nil {
				t.Fatal("explicit empty accepted")
			}
		})
	}
}
