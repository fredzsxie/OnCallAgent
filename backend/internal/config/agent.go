package config

import (
	"fmt"
	"strconv"
	"time"
)

type ChatAgent struct {
	MaxSteps int
	Timeout  time.Duration
}

func LoadChatAgent() (ChatAgent, error) {
	c := ChatAgent{}
	var err error
	if c.MaxSteps, err = strconv.Atoi(env("ONCALL_CHAT_AGENT_MAX_STEPS", "12")); err != nil {
		return c, fmt.Errorf("ONCALL_CHAT_AGENT_MAX_STEPS must be a positive integer")
	}
	if c.Timeout, err = duration("ONCALL_CHAT_AGENT_TIMEOUT", "60s"); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c ChatAgent) Validate() error {
	if c.MaxSteps < 1 {
		return fmt.Errorf("ONCALL_CHAT_AGENT_MAX_STEPS must be positive")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("ONCALL_CHAT_AGENT_TIMEOUT must be positive")
	}
	return nil
}
