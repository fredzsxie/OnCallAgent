package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type OpsAgent struct {
	MaxIterations    int
	ExecutorMaxSteps int
	Timeout          time.Duration
}

func LoadOpsAgent() (OpsAgent, error) {
	c := OpsAgent{}
	var err error
	if c.MaxIterations, err = strconv.Atoi(env("ONCALL_OPS_MAX_ITERATIONS", "8")); err != nil {
		return c, fmt.Errorf("ONCALL_OPS_MAX_ITERATIONS must be a positive integer")
	}
	if c.ExecutorMaxSteps, err = strconv.Atoi(env("ONCALL_OPS_EXECUTOR_MAX_STEPS", "12")); err != nil {
		return c, fmt.Errorf("ONCALL_OPS_EXECUTOR_MAX_STEPS must be a positive integer")
	}
	if c.Timeout, err = duration("ONCALL_OPS_TIMEOUT", "180s"); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c OpsAgent) Validate() error {
	if c.MaxIterations < 1 || c.ExecutorMaxSteps < 1 || c.Timeout <= 0 {
		return fmt.Errorf("ops iteration limits and timeout must be positive")
	}
	return nil
}

func LoadOpsModels() (planner, executor ChatModel, err error) {
	if planner, err = loadOpsModel("ONCALL_OPS_PLANNER"); err != nil {
		return
	}
	executor, err = loadOpsModel("ONCALL_OPS_EXECUTOR")
	return
}

func loadOpsModel(prefix string) (ChatModel, error) {
	// Merge before validation so a complete dedicated model needs no ordinary chat credentials.
	value := func(suffix, fallback string) string {
		if _, ok := os.LookupEnv(prefix + suffix); ok {
			return env(prefix+suffix, "")
		}
		return env("ONCALL_CHAT_MODEL"+suffix, fallback)
	}
	c := ChatModel{BaseURL: value("_BASE_URL", ""), Model: value("_MODEL", ""), APIKey: value("_API_KEY", "")}
	var err error
	if c.Timeout, err = time.ParseDuration(value("_TIMEOUT", "30s")); err != nil || c.Validate() != nil {
		return c, fmt.Errorf("%s model configuration is invalid", prefix)
	}
	return c, nil
}
