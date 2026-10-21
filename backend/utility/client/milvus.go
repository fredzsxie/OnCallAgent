package client

import (
	"context"
	"errors"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

func NewMilvus(ctx context.Context, cfg config.Milvus) (out client.Client, err error) {
	root := ctx
	started := time.Now()
	var cause error
	logger.Info(ctx, "Milvus", "开始建立连接", logger.F("tls", cfg.TLS))
	defer func() {
		if cause != nil {
			logger.Finish(ctx, "Milvus", "连接初始化", started, cause)
		} else {
			logger.Finish(ctx, "Milvus", "连接初始化", started, err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	connection, err := client.NewClient(ctx, client.Config{
		Address: cfg.Address, DBName: cfg.Database, Username: cfg.Username, Password: cfg.Password, EnableTLSAuth: cfg.TLS,
	})
	if err != nil {
		cause = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Milvus connection failed; check endpoint, database, authentication and TLS")
	}
	return &loggedMilvus{Client: connection, ctx: root}, nil
}

type loggedMilvus struct {
	client.Client
	ctx context.Context
}

func (c *loggedMilvus) Close() error {
	started := time.Now()
	err := c.Client.Close()
	logger.Finish(c.ctx, "Milvus", "连接关闭", started, err)
	return err
}
