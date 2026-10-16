package client

import (
	"context"
	"errors"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

const MilvusSDKVersion = "v2.4.2"

func NewMilvus(ctx context.Context, cfg config.Milvus) (client.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	connection, err := client.NewClient(ctx, client.Config{
		Address: cfg.Address, DBName: cfg.Database, Username: cfg.Username, Password: cfg.Password, EnableTLSAuth: cfg.TLS,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Milvus connection failed; check endpoint, database, authentication and TLS")
	}
	return connection, nil
}
