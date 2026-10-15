package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

type Config struct {
	Port      int
	ChatModel ChatModel
}

type ChatModel struct {
	BaseURL string
	Model   string
	APIKey  string `json:"-"`
	Timeout time.Duration
}

func Load(ctx context.Context) (Config, error) {
	value, err := g.Cfg().Get(ctx, "server.port", 6872)
	if err != nil {
		return Config{}, err
	}
	rawPort := value.String()
	// An explicitly empty variable must fail validation rather than fall back to YAML.
	if port, ok := os.LookupEnv("ONCALL_SERVER_PORT"); ok {
		rawPort = port
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("server.port / ONCALL_SERVER_PORT must be an integer between 1 and 65535")
	}
	model, err := LoadChatModel(ctx)
	if err != nil {
		return Config{}, err
	}
	return Config{Port: port, ChatModel: model}, nil
}

func LoadChatModel(ctx context.Context) (ChatModel, error) {
	var cfg ChatModel
	for _, field := range []struct {
		key, env, fallback string
		target             *string
	}{
		{"chat_model.base_url", "ONCALL_CHAT_MODEL_BASE_URL", "", &cfg.BaseURL},
		{"chat_model.model", "ONCALL_CHAT_MODEL_MODEL", "", &cfg.Model},
	} {
		value, err := g.Cfg().Get(ctx, field.key, field.fallback)
		if err != nil {
			return ChatModel{}, err
		}
		*field.target = value.String()
		if override, ok := os.LookupEnv(field.env); ok {
			*field.target = override
		}
		*field.target = strings.TrimSpace(*field.target)
	}
	value, err := g.Cfg().Get(ctx, "chat_model.timeout", "30s")
	if err != nil {
		return ChatModel{}, err
	}
	rawTimeout := value.String()
	if override, ok := os.LookupEnv("ONCALL_CHAT_MODEL_TIMEOUT"); ok {
		rawTimeout = override
	}
	cfg.Timeout, err = time.ParseDuration(rawTimeout)
	if err != nil || cfg.Timeout <= 0 {
		return ChatModel{}, fmt.Errorf("chat_model.timeout / ONCALL_CHAT_MODEL_TIMEOUT must be a positive duration, for example 30s")
	}
	// Read secrets only from the environment, optionally populated by .env, never YAML.
	keyInFile, err := g.Cfg().Get(ctx, "chat_model.api_key")
	if err != nil {
		return ChatModel{}, err
	}
	if !keyInFile.IsNil() {
		return ChatModel{}, fmt.Errorf("chat_model.api_key must not be configured in YAML; use ONCALL_CHAT_MODEL_API_KEY")
	}
	cfg.APIKey = os.Getenv("ONCALL_CHAT_MODEL_API_KEY")
	if err := cfg.Validate(); err != nil {
		return ChatModel{}, err
	}
	return cfg, nil
}

func (c ChatModel) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("chat_model.base_url is required; set ONCALL_CHAT_MODEL_BASE_URL in the environment or .env")
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("chat_model.base_url must be an http(s) API base URL without credentials, query or fragment")
	}
	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/chat/completions") {
		// Eino appends this endpoint itself; accepting it here would duplicate the path.
		return fmt.Errorf("chat_model.base_url must be the API base path, not /chat/completions")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("chat_model.model / ONCALL_CHAT_MODEL_MODEL is required")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("ONCALL_CHAT_MODEL_API_KEY is required")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("chat_model.timeout must be positive")
	}
	return nil
}
