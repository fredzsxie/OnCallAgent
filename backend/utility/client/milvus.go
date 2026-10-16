package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const MilvusSDKVersion = "v2.4.2"

func NewMilvus(ctx context.Context, cfg config.Milvus) (client.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	c := client.Config{Address: cfg.Address, DBName: cfg.Database, Username: cfg.Username, Password: cfg.Password, APIKey: cfg.Token, EnableTLSAuth: cfg.TLS}
	if cfg.TLS {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}
		if cfg.CAFile != "" {
			pem, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, errors.New("cannot read Milvus CA certificate")
			}
			pool, err := x509.SystemCertPool()
			if err != nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("invalid Milvus CA certificate")
			}
			tlsConfig.RootCAs = pool
		}
		c.DialOptions = []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	connection, err := client.NewClient(ctx, c)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Milvus connection failed; check endpoint, database, authentication and TLS")
	}
	return connection, nil
}
