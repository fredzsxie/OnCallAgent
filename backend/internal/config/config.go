package config

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/gogf/gf/v2/frame/g"
)

type Config struct {
	Port int
}

// Load reads GoFrame configuration, then explicitly applies the environment override.
func Load(ctx context.Context) (Config, error) {
	value, err := g.Cfg().Get(ctx, "server.port", 6872)
	if err != nil {
		return Config{}, err
	}
	rawPort := value.String()
	if port, ok := os.LookupEnv("ONCALL_SERVER_PORT"); ok {
		rawPort = port
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("server.port / ONCALL_SERVER_PORT must be an integer between 1 and 65535")
	}
	return Config{Port: port}, nil
}
