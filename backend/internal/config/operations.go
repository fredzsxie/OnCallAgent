package config

import (
	"fmt"
	"net/http"
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
	HeaderName  string
	HeaderValue string `json:"-"`
}

type ToolHTTP struct {
	URL     string
	Timeout time.Duration
	Auth    HTTPAuth
}

type LogMCP struct {
	HTTP     ToolHTTP
	Region   string
	TopicID  string
	Lookback time.Duration
	Limit    int
}

func loadToolHTTP(prefix, address string, customHeader bool) (ToolHTTP, error) {
	c := ToolHTTP{URL: env(prefix+"_BASE_URL", address)}
	if prefix == "ONCALL_LOG_MCP" {
		c.URL = env(prefix+"_URL", address)
	}
	var err error
	if c.Timeout, err = duration(prefix+"_TIMEOUT", "15s"); err != nil {
		return c, err
	}
	c.Auth = HTTPAuth{Type: env(prefix+"_AUTH_TYPE", "none"), Username: env(prefix+"_USERNAME", ""),
		Password: os.Getenv(prefix + "_PASSWORD"), BearerToken: os.Getenv(prefix + "_BEARER_TOKEN"),
		HeaderName: env(prefix+"_HEADER_NAME", ""), HeaderValue: os.Getenv(prefix + "_HEADER_VALUE")}
	return c, c.Validate(customHeader)
}

func LoadPrometheus() (ToolHTTP, error) {
	return loadToolHTTP("ONCALL_PROMETHEUS", "http://127.0.0.1:9090", false)
}

func LoadLogMCP() (LogMCP, error) {
	c := LogMCP{Region: env("ONCALL_LOG_REGION", ""), TopicID: env("ONCALL_LOG_TOPIC_ID", "")}
	var err error
	if c.HTTP, err = loadToolHTTP("ONCALL_LOG_MCP", "", true); err != nil {
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

func (c ToolHTTP) Validate(customHeader bool) error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("tool URL must be an http(s) URL without credentials, query or fragment")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("tool timeout must be positive")
	}
	return c.Auth.Validate(customHeader)
}

func (c LogMCP) Validate() error {
	if err := c.HTTP.Validate(true); err != nil {
		return err
	}
	if c.Lookback <= 0 || c.Lookback/time.Millisecond == 0 || c.Limit < 1 || c.Limit > 1000 {
		return fmt.Errorf("log lookback must be at least 1ms and limit must be between 1 and 1000")
	}
	return nil
}

func (a HTTPAuth) Validate(customHeader bool) error {
	for _, value := range []string{a.Username, a.Password, a.BearerToken, a.HeaderName, a.HeaderValue} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("authentication values must not contain newlines")
		}
	}
	switch a.Type {
	case "none":
		if a.Username != "" || a.Password != "" || a.BearerToken != "" || a.HeaderName != "" || a.HeaderValue != "" {
			return fmt.Errorf("authentication fields require an explicit auth type")
		}
	case "basic":
		if a.Username == "" || a.Password == "" || strings.Contains(a.Username, ":") || a.BearerToken != "" || a.HeaderName != "" || a.HeaderValue != "" {
			return fmt.Errorf("basic auth requires only username and password")
		}
	case "bearer":
		if strings.TrimSpace(a.BearerToken) == "" || a.Username != "" || a.Password != "" || a.HeaderName != "" || a.HeaderValue != "" {
			return fmt.Errorf("bearer auth requires only a token")
		}
	case "header":
		if !customHeader || a.HeaderName == "" || strings.TrimSpace(a.HeaderValue) == "" || a.Username != "" || a.Password != "" || a.BearerToken != "" {
			return fmt.Errorf("header auth requires only a header name and value")
		}
		for _, r := range a.HeaderName {
			if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", r) {
				return fmt.Errorf("invalid authentication header name")
			}
		}
		switch http.CanonicalHeaderKey(a.HeaderName) {
		case "Host", "Connection", "Content-Type", "Content-Length", "Transfer-Encoding", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version":
			return fmt.Errorf("authentication header must not override transport headers")
		}
	default:
		return fmt.Errorf("unsupported authentication type")
	}
	return nil
}
