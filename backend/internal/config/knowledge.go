package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

type Embedding struct {
	BaseURL    string
	Model      string
	APIKey     string `json:"-"`
	Timeout    time.Duration
	Dimensions int
}

type Milvus struct {
	Address        string
	Database       string
	Collection     string
	Username       string `json:"-"`
	Password       string `json:"-"`
	Token          string `json:"-"`
	TLS            bool
	CAFile         string
	ServerName     string
	ConnectTimeout time.Duration
	Timeout        time.Duration
}

func setting(ctx context.Context, key, env, fallback string) (string, error) {
	v, err := g.Cfg().Get(ctx, key, fallback)
	if err != nil {
		return "", err
	}
	if value, ok := os.LookupEnv(env); ok {
		return strings.TrimSpace(value), nil
	}
	return strings.TrimSpace(v.String()), nil
}

func secret(ctx context.Context, key, env string) (string, error) {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil {
		return "", err
	}
	if !v.IsNil() {
		return "", fmt.Errorf("%s must not be configured in YAML; use %s", key, env)
	}
	return os.Getenv(env), nil
}

func duration(ctx context.Context, key, env, fallback string) (time.Duration, error) {
	v, err := setting(ctx, key, env, fallback)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s / %s must be a positive duration", key, env)
	}
	return d, nil
}

func LoadEmbedding(ctx context.Context) (Embedding, error) {
	var c Embedding
	var err error
	if c.BaseURL, err = setting(ctx, "embedding.base_url", "ONCALL_EMBEDDING_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3"); err != nil {
		return c, err
	}
	if c.Model, err = setting(ctx, "embedding.model", "ONCALL_EMBEDDING_MODEL", "doubao-embedding-vision-251215"); err != nil {
		return c, err
	}
	if c.Timeout, err = duration(ctx, "embedding.timeout", "ONCALL_EMBEDDING_TIMEOUT", "30s"); err != nil {
		return c, err
	}
	raw, err := setting(ctx, "embedding.dimensions", "ONCALL_EMBEDDING_DIMENSIONS", "0")
	if err != nil {
		return c, err
	}
	if c.Dimensions, err = strconv.Atoi(raw); err != nil {
		return c, fmt.Errorf("embedding.dimensions must be an integer (0 means discover)")
	}
	if c.APIKey, err = secret(ctx, "embedding.api_key", "ONCALL_EMBEDDING_API_KEY"); err != nil {
		return c, err
	}
	// An explicitly empty ONCALL value must not silently fall back to another key.
	if _, ok := os.LookupEnv("ONCALL_EMBEDDING_API_KEY"); !ok {
		c.APIKey = os.Getenv("ARK_API_KEY")
	}
	return c, c.Validate()
}

func (c Embedding) Validate() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, "/embeddings") {
		return fmt.Errorf("embedding.base_url must be an http(s) API base URL without credentials, query, fragment or /embeddings suffix")
	}
	if strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("embedding.model and ONCALL_EMBEDDING_API_KEY (or ARK_API_KEY) are required")
	}
	if c.Timeout <= 0 || c.Dimensions < 0 || c.Dimensions > 32768 {
		return fmt.Errorf("embedding.timeout must be positive; dimensions must be 0 (discover) or between 1 and 32768")
	}
	return nil
}

func LoadMilvus(ctx context.Context) (Milvus, error) {
	var c Milvus
	for _, f := range []struct {
		key, env, fallback string
		target             *string
	}{
		{"milvus.address", "ONCALL_MILVUS_ADDRESS", "localhost:19530", &c.Address},
		{"milvus.database", "ONCALL_MILVUS_DATABASE", "default", &c.Database},
		{"milvus.collection", "ONCALL_MILVUS_COLLECTION", "oncall_knowledge", &c.Collection},
		{"milvus.ca_file", "ONCALL_MILVUS_CA_FILE", "", &c.CAFile},
		{"milvus.server_name", "ONCALL_MILVUS_SERVER_NAME", "", &c.ServerName},
	} {
		v, err := setting(ctx, f.key, f.env, f.fallback)
		if err != nil {
			return c, err
		}
		*f.target = v
	}
	for _, f := range []struct {
		key, env string
		target   *string
	}{
		{"milvus.username", "ONCALL_MILVUS_USERNAME", &c.Username},
		{"milvus.password", "ONCALL_MILVUS_PASSWORD", &c.Password},
		{"milvus.token", "ONCALL_MILVUS_TOKEN", &c.Token},
	} {
		v, err := secret(ctx, f.key, f.env)
		if err != nil {
			return c, err
		}
		*f.target = v
	}
	raw, err := setting(ctx, "milvus.tls", "ONCALL_MILVUS_TLS", "false")
	if err != nil {
		return c, err
	}
	if c.TLS, err = strconv.ParseBool(raw); err != nil {
		return c, fmt.Errorf("milvus.tls / ONCALL_MILVUS_TLS must be a boolean")
	}
	if c.ConnectTimeout, err = duration(ctx, "milvus.connect_timeout", "ONCALL_MILVUS_CONNECT_TIMEOUT", "5s"); err != nil {
		return c, err
	}
	if c.Timeout, err = duration(ctx, "milvus.timeout", "ONCALL_MILVUS_TIMEOUT", "60s"); err != nil {
		return c, err
	}
	return c, c.Validate()
}

var milvusName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,254}$`)

func (c Milvus) Validate() error {
	host, port, err := net.SplitHostPort(c.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || strings.ContainsAny(host, "/@?# \t\n") || portErr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("milvus.address must be host:port without credentials or URL components")
	}
	if !milvusName.MatchString(c.Database) || !milvusName.MatchString(c.Collection) {
		return fmt.Errorf("milvus.database and collection must be valid identifiers (maximum 255 characters)")
	}
	if (c.Username == "") != (c.Password == "") || (c.Token != "" && (c.Username != "" || c.Password != "")) {
		return fmt.Errorf("milvus authentication requires either username + password, token, or neither")
	}
	if !c.TLS && (c.CAFile != "" || c.ServerName != "") {
		return fmt.Errorf("milvus.ca_file and server_name require milvus.tls=true")
	}
	if c.ConnectTimeout <= 0 || c.Timeout <= 0 {
		return fmt.Errorf("milvus timeouts must be positive")
	}
	return nil
}
