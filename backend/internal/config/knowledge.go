package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Embedding struct {
	BaseURL string
	Model   string
	APIKey  string `json:"-"`
	Timeout time.Duration
}

type Milvus struct {
	Address        string
	Database       string
	Collection     string
	Username       string `json:"-"`
	Password       string `json:"-"`
	TLS            bool
	ConnectTimeout time.Duration
	Timeout        time.Duration
}

func LoadEmbedding() (Embedding, error) {
	c := Embedding{
		BaseURL: env("ONCALL_EMBEDDING_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3"),
		Model:   env("ONCALL_EMBEDDING_MODEL", "doubao-embedding-vision-251215"),
		APIKey:  os.Getenv("ARK_API_KEY"),
	}
	if key, ok := os.LookupEnv("ONCALL_EMBEDDING_API_KEY"); ok {
		c.APIKey = key
	}
	var err error
	if c.Timeout, err = duration("ONCALL_EMBEDDING_TIMEOUT", "30s"); err != nil {
		return c, err
	}
	if err = validateBaseURL("ONCALL_EMBEDDING_BASE_URL", c.BaseURL, "/embeddings"); err != nil {
		return c, err
	}
	if c.Model == "" || strings.TrimSpace(c.APIKey) == "" {
		return c, fmt.Errorf("ONCALL_EMBEDDING_MODEL and ONCALL_EMBEDDING_API_KEY (or ARK_API_KEY) are required")
	}
	return c, nil
}

func LoadMilvus() (Milvus, error) {
	c := Milvus{
		Address:    env("ONCALL_MILVUS_ADDRESS", "localhost:19530"),
		Database:   env("ONCALL_MILVUS_DATABASE", "default"),
		Collection: env("ONCALL_MILVUS_COLLECTION", "oncall_knowledge"),
		Username:   env("ONCALL_MILVUS_USERNAME", ""), Password: os.Getenv("ONCALL_MILVUS_PASSWORD"),
	}
	var err error
	if c.TLS, err = strconv.ParseBool(env("ONCALL_MILVUS_TLS", "false")); err != nil {
		return c, fmt.Errorf("ONCALL_MILVUS_TLS must be a boolean")
	}
	if c.ConnectTimeout, err = duration("ONCALL_MILVUS_CONNECT_TIMEOUT", "5s"); err != nil {
		return c, err
	}
	if c.Timeout, err = duration("ONCALL_MILVUS_TIMEOUT", "60s"); err != nil {
		return c, err
	}
	host, port, err := net.SplitHostPort(c.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || strings.ContainsAny(host, "/@?# \t\n") || portErr != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("ONCALL_MILVUS_ADDRESS must be host:port")
	}
	if c.Database == "" || c.Collection == "" {
		return c, fmt.Errorf("ONCALL_MILVUS_DATABASE and ONCALL_MILVUS_COLLECTION are required")
	}
	if (c.Username == "") != (c.Password == "") {
		return c, fmt.Errorf("Milvus authentication requires both username and password, or neither")
	}
	return c, nil
}
