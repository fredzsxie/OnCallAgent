package config

import (
	"fmt"
	"strconv"
	"strings"


)

type Logging struct {
	Level         string
	FileEnabled   bool
	Dir           string
	RetentionDays int
}

func LoadLogging() (Logging, error) {
	c := Logging{Level: strings.ToLower(env("ONCALL_LOG_LEVEL", "info")), Dir: env("ONCALL_LOG_DIR", "./log")}
	var err error
	if c.FileEnabled, err = strconv.ParseBool(env("ONCALL_LOG_FILE_ENABLED", "true")); err != nil {
		return c, fmt.Errorf("ONCALL_LOG_FILE_ENABLED must be a boolean")
	}
	if c.RetentionDays, err = strconv.Atoi(env("ONCALL_LOG_RETENTION_DAYS", "7")); err != nil {
		return c, fmt.Errorf("ONCALL_LOG_RETENTION_DAYS must be a positive integer")
	}
	return c, c.Validate()
}

func (c Logging) Validate() error {
	switch c.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("ONCALL_LOG_LEVEL must be debug, info, warn or error")
	}
	if strings.TrimSpace(c.Dir) == "" || strings.ContainsAny(c.Dir, "\x00\r\n") {
		return fmt.Errorf("ONCALL_LOG_DIR must be a nonempty directory")
	}
	if c.RetentionDays <= 0 || c.RetentionDays > 36500 {
		return fmt.Errorf("ONCALL_LOG_RETENTION_DAYS must be between 1 and 36500")
	}
	return nil
}
