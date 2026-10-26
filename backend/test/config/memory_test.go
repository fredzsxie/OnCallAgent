package config_test

import (
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"testing"
	"time"
)

func TestMemoryConfig(t *testing.T) {
	unsetEnv(t, "ONCALL_CHAT_MEMORY_MAX_ROUNDS")
	unsetEnv(t, "ONCALL_CHAT_MEMORY_TTL")
	cfg, err := config.LoadChatMemory()
	if err != nil || cfg.MaxRounds != 3 || cfg.TTL != 30*time.Minute {
		t.Fatalf("defaults=%+v %v", cfg, err)
	}
	changeDir(t, t.TempDir())
	writeEnv(t, ".env", "ONCALL_CHAT_MEMORY_MAX_ROUNDS=5\nONCALL_CHAT_MEMORY_TTL=10m\n")
	t.Setenv("ONCALL_CHAT_MEMORY_MAX_ROUNDS", "2")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadChatMemory()
	if err != nil || cfg.MaxRounds != 2 || cfg.TTL != 10*time.Minute {
		t.Fatalf("overrides=%+v %v", cfg, err)
	}
	for _, key := range []string{"ONCALL_CHAT_MEMORY_MAX_ROUNDS", "ONCALL_CHAT_MEMORY_TTL"} {
		for _, value := range []string{"", "0", "-1", "NaN", "1.5"} {
			t.Run(key+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := config.LoadChatMemory(); err == nil {
					t.Fatal("invalid memory configuration accepted")
				}
			})
		}
	}
}
