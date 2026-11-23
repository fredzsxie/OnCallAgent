package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host      string
	Port      int
	ChatModel ChatModel
}

type ChatModel struct {
	BaseURL string
	Model   string
	APIKey  string `json:"-"`
	Timeout time.Duration
}

func env(name, fallback string) string {
	// An explicitly empty variable must reach validation rather than use a default.
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func duration(name, fallback string) (time.Duration, error) {
	value, err := time.ParseDuration(env(name, fallback))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func Load() (Config, error) {
	host := env("ONCALL_SERVER_HOST", "")
	if _, configured := os.LookupEnv("ONCALL_SERVER_HOST"); configured && net.ParseIP(host) == nil {
		return Config{}, fmt.Errorf("ONCALL_SERVER_HOST must be an IPv4 or IPv6 address")
	}
	port, err := strconv.Atoi(env("ONCALL_SERVER_PORT", "6872"))
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("ONCALL_SERVER_PORT must be an integer between 1 and 65535")
	}
	model, err := LoadChatModel()
	return Config{Host: host, Port: port, ChatModel: model}, err
}

// ListenAddress preserves wildcard binding by default and brackets IPv6 addresses.
func (c Config) ListenAddress() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func LoadChatModel() (ChatModel, error) {
	c := ChatModel{BaseURL: env("ONCALL_CHAT_MODEL_BASE_URL", ""), Model: env("ONCALL_CHAT_MODEL_MODEL", ""), APIKey: os.Getenv("ONCALL_CHAT_MODEL_API_KEY")}
	var err error
	if c.Timeout, err = duration("ONCALL_CHAT_MODEL_TIMEOUT", "30s"); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func validateBaseURL(name, value, endpoint string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, endpoint) {
		return fmt.Errorf("%s must be an http(s) API base URL without credentials, query, fragment or completion/embedding endpoint", name)
	}
	return nil
}

func (c ChatModel) Validate() error {
	if err := validateBaseURL("ONCALL_CHAT_MODEL_BASE_URL", c.BaseURL, "/chat/completions"); err != nil {
		return err
	}
	if strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("ONCALL_CHAT_MODEL_MODEL and ONCALL_CHAT_MODEL_API_KEY are required")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("ONCALL_CHAT_MODEL_TIMEOUT must be positive")
	}
	return nil
}
