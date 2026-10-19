package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type HTTPAuth struct {
	Type        string
	Username    string
	Password    string `json:"-"`
	BearerToken string `json:"-"`
}

type ToolHTTP struct {
	URL     string
	Timeout time.Duration
	Auth    HTTPAuth
}

type LogMCP struct {
	Command     string
	Args        []string
	Timeout     time.Duration
	SecretID    string `json:"-"`
	SecretKey   string `json:"-"`
	APIBaseHost string
	TimeZone    string
	Region      string
	TopicID     string
	Lookback    time.Duration
	Limit       int
}

func loadToolHTTP(prefix, address string) (ToolHTTP, error) {
	c := ToolHTTP{URL: env(prefix+"_BASE_URL", address)}
	var err error
	if c.Timeout, err = duration(prefix+"_TIMEOUT", "15s"); err != nil {
		return c, err
	}
	c.Auth = HTTPAuth{Type: env(prefix+"_AUTH_TYPE", "none"), Username: env(prefix+"_USERNAME", ""),
		Password: os.Getenv(prefix + "_PASSWORD"), BearerToken: os.Getenv(prefix + "_BEARER_TOKEN")}
	return c, c.Validate()
}

func LoadPrometheus() (ToolHTTP, error) {
	return loadToolHTTP("ONCALL_PROMETHEUS", "http://127.0.0.1:9090")
}

func LoadLogMCP() (LogMCP, error) {
	argsJSON := env("ONCALL_LOG_MCP_ARGS_JSON", `["-y","cls-mcp-server@1.2.0"]`)
	var args []string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args == nil {
		return LogMCP{}, fmt.Errorf("ONCALL_LOG_MCP_ARGS_JSON must be a JSON string array")
	}
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return LogMCP{}, fmt.Errorf("ONCALL_LOG_MCP_ARGS_JSON contains an invalid argument")
		}
	}
	c := LogMCP{
		Command: env("ONCALL_LOG_MCP_COMMAND", "npx"), Args: args,
		SecretID: os.Getenv("TENCENTCLOUD_SECRET_ID"), SecretKey: os.Getenv("TENCENTCLOUD_SECRET_KEY"),
		APIBaseHost: env("TENCENTCLOUD_API_BASE_HOST", ""),
		TimeZone:    env("ONCALL_TIME_ZONE", "Asia/Shanghai"),
		Region:      env("ONCALL_LOG_REGION", ""), TopicID: env("ONCALL_LOG_TOPIC_ID", ""),
	}
	var err error
	if c.Timeout, err = duration("ONCALL_LOG_MCP_TIMEOUT", "15s"); err != nil {
		return c, err
	}
	if c.Lookback, err = duration("ONCALL_LOG_LOOKBACK", "15m"); err != nil {
		return c, err
	}
	if c.Limit, err = strconv.Atoi(env("ONCALL_LOG_LIMIT", "20")); err != nil {
		return c, fmt.Errorf("ONCALL_LOG_LIMIT must be an integer")
	}
	return c, c.Validate()
}

func (c ToolHTTP) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("tool URL must be an http(s) URL without credentials, query or fragment")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("tool timeout must be positive")
	}
	return c.Auth.Validate()
}

func (c LogMCP) Validate() error {
	if strings.TrimSpace(c.Command) == "" || c.Timeout <= 0 || c.Lookback <= 0 || c.Lookback/time.Millisecond == 0 || c.Limit < 1 || c.Limit > 1000 {
		return fmt.Errorf("log MCP command, timeout, lookback or limit is invalid")
	}
	for _, arg := range c.Args {
		if strings.TrimSpace(arg) == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("log MCP argument is invalid")
		}
	}
	if strings.TrimSpace(c.SecretID) == "" || strings.TrimSpace(c.SecretKey) == "" {
		return fmt.Errorf("TENCENTCLOUD_SECRET_ID and TENCENTCLOUD_SECRET_KEY are required when the CLS tool is enabled")
	}
	if strings.ContainsAny(c.APIBaseHost, "\x00\r\n /\\") {
		return fmt.Errorf("TENCENTCLOUD_API_BASE_HOST must be a host name")
	}
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return fmt.Errorf("ONCALL_TIME_ZONE must be a valid IANA time zone")
	}
	return nil
}

func (a HTTPAuth) Validate() error {
	if strings.ContainsAny(a.Username+a.Password+a.BearerToken, "\r\n") {
		return fmt.Errorf("authentication values must not contain newlines")
	}
	switch a.Type {
	case "none":
		if a.Username != "" || a.Password != "" || a.BearerToken != "" {
			return fmt.Errorf("authentication fields require an explicit auth type")
		}
	case "basic":
		if a.Username == "" || a.Password == "" || strings.Contains(a.Username, ":") || a.BearerToken != "" {
			return fmt.Errorf("basic auth requires only username and password")
		}
	case "bearer":
		if strings.TrimSpace(a.BearerToken) == "" || a.Username != "" || a.Password != "" {
			return fmt.Errorf("bearer auth requires only a token")
		}
	default:
		return fmt.Errorf("unsupported authentication type")
	}
	return nil
}
