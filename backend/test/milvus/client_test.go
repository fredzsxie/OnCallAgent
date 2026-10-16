package milvus_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

type server struct {
	milvuspb.UnimplementedMilvusServiceServer
}

func (server) Connect(context.Context, *milvuspb.ConnectRequest) (*milvuspb.ConnectResponse, error) {
	return &milvuspb.ConnectResponse{Status: &commonpb.Status{}}, nil
}
func (server) GetVersion(context.Context, *milvuspb.GetVersionRequest) (*milvuspb.GetVersionResponse, error) {
	return &milvuspb.GetVersionResponse{Status: &commonpb.Status{}, Version: "v2.4.1"}, nil
}

func TestSDKAuthenticationDatabaseAndTLS(t *testing.T) {
	for _, mode := range []string{"none", "password", "token", "tls"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.Milvus{Database: "other_db", Collection: "test", Timeout: time.Second, ConnectTimeout: time.Second}
			wantAuth := ""
			if mode == "password" {
				cfg.Username = "user"
				cfg.Password = "fixture-password"
				wantAuth = base64.StdEncoding.EncodeToString([]byte("user:fixture-password"))
			}
			if mode == "token" {
				cfg.Token = "fixture-token"
				wantAuth = base64.StdEncoding.EncodeToString([]byte(cfg.Token))
			}
			options := []grpc.ServerOption{grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				md, _ := metadata.FromIncomingContext(ctx)
				if dbs := md.Get("dbname"); len(dbs) != 1 || dbs[0] != "other_db" {
					t.Error("database metadata was not propagated")
				}
				auth := md.Get("authorization")
				if wantAuth != "" && (len(auth) != 1 || auth[0] != wantAuth) {
					t.Error("incorrect authentication metadata")
				}
				return handler(ctx, req)
			})}
			if mode == "tls" {
				h := httptest.NewTLSServer(nil)
				cert := h.TLS.Certificates[0]
				h.Close()
				cfg.TLS = true
				cfg.ServerName = "example.com"
				cfg.CAFile = filepath.Join(t.TempDir(), "ca.pem")
				if err := os.WriteFile(cfg.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
					t.Fatal(err)
				}
				options = append(options, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			s := grpc.NewServer(options...)
			milvuspb.RegisterMilvusServiceServer(s, &server{})
			go s.Serve(listener)
			t.Cleanup(s.Stop)
			cfg.Address = listener.Addr().String()
			c, err := client.NewMilvus(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			v, err := c.GetVersion(context.Background())
			if err != nil || v != "v2.4.1" {
				t.Fatalf("SDK call failed: %v", err)
			}
		})
	}
}
