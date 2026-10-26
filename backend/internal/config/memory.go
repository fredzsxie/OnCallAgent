package config

import (
	"fmt"
	"strconv"
	"time"
)

type ChatMemory struct {
	MaxRounds int
	TTL       time.Duration
}

func LoadChatMemory() (ChatMemory, error) {
	c := ChatMemory{}
	var err error
	if c.MaxRounds, err = strconv.Atoi(env("ONCALL_CHAT_MEMORY_MAX_ROUNDS", "3")); err != nil {
		return c, fmt.Errorf("ONCALL_CHAT_MEMORY_MAX_ROUNDS must be a positive integer")
	}
	if c.TTL, err = duration("ONCALL_CHAT_MEMORY_TTL", "30m"); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c ChatMemory) Validate() error {
	if c.MaxRounds < 1 {
		return fmt.Errorf("ONCALL_CHAT_MEMORY_MAX_ROUNDS must be positive")
	}
	if c.TTL <= 0 {
		return fmt.Errorf("ONCALL_CHAT_MEMORY_TTL must be positive")
	}
	return nil
}
