package config_test

import (
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"testing"
	"time"
)

func TestChatAgentConfiguration(t *testing.T) {
	unsetEnv(t, "ONCALL_CHAT_AGENT_MAX_STEPS")
	unsetEnv(t, "ONCALL_CHAT_AGENT_TIMEOUT")
	cfg, err := config.LoadChatAgent()
	if err != nil || cfg.MaxSteps != 12 || cfg.Timeout != 60*time.Second {
		t.Fatalf("defaults=%+v %v", cfg, err)
	}
	t.Setenv("ONCALL_CHAT_AGENT_MAX_STEPS", "5")
	t.Setenv("ONCALL_CHAT_AGENT_TIMEOUT", "2s")
	cfg, err = config.LoadChatAgent()
	if err != nil || cfg.MaxSteps != 5 || cfg.Timeout != 2*time.Second {
		t.Fatalf("overrides=%+v %v", cfg, err)
	}
	for _, value := range []string{"", "0", "-1", "NaN", "1.2"} {
		t.Run("steps="+value, func(t *testing.T) {
			t.Setenv("ONCALL_CHAT_AGENT_MAX_STEPS", value)
			if _, err := config.LoadChatAgent(); err == nil {
				t.Fatal("bad step count accepted")
			}
		})
	}
	for _, value := range []string{"", "0s", "-1s", "NaN", "1"} {
		t.Run("timeout="+value, func(t *testing.T) {
			t.Setenv("ONCALL_CHAT_AGENT_TIMEOUT", value)
			if _, err := config.LoadChatAgent(); err == nil {
				t.Fatal("bad timeout accepted")
			}
		})
	}
}

func TestChatAgentEnvPriority(t *testing.T) {
	changeDir(t, t.TempDir())
	unsetEnv(t, "ONCALL_CHAT_AGENT_MAX_STEPS")
	unsetEnv(t, "ONCALL_CHAT_AGENT_TIMEOUT")
	writeEnv(t, ".env", "ONCALL_CHAT_AGENT_MAX_STEPS=9\nONCALL_CHAT_AGENT_TIMEOUT=20s\n")
	t.Setenv("ONCALL_CHAT_AGENT_MAX_STEPS", "4")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadChatAgent()
	if err != nil || cfg.MaxSteps != 4 || cfg.Timeout != 20*time.Second {
		t.Fatalf("config=%+v %v", cfg, err)
	}
}
