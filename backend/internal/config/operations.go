package config

import (
	"fmt"
	"net/url"
	"os"
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
