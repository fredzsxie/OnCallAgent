package milvus_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http/httptest"
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

func TestSDKAuthenticationAndDatabase(t *testing.T) {
	for _, password := range []bool{false, true} {
		cfg := config.Milvus{Database: "other_db", Collection: "test", ConnectTimeout: time.Second, Timeout: time.Second}
		wantAuth := ""
		if password {
			cfg.Username = "user"
			cfg.Password = "fixture-password"
			wantAuth = base64.StdEncoding.EncodeToString([]byte("user:fixture-password"))
		}
		s := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			if dbs := md.Get("dbname"); len(dbs) != 1 || dbs[0] != "other_db" {
				t.Error("database metadata was not propagated")
			}
			auth := md.Get("authorization")
			if (wantAuth == "" && len(auth) != 0) || (wantAuth != "" && (len(auth) != 1 || auth[0] != wantAuth)) {
				t.Error("incorrect authentication metadata")
			}
			return handler(ctx, req)
		}))
		milvuspb.RegisterMilvusServiceServer(s, &server{})
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go s.Serve(listener)
		t.Cleanup(s.Stop)
		cfg.Address = listener.Addr().String()
		c, err := client.NewMilvus(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		v, err := c.GetVersion(context.Background())
		c.Close()
		if err != nil || v != "v2.4.1" {
			t.Fatalf("SDK call failed: %v", err)
		}
	}
}

func TestTLSRejectsUntrustedCertificate(t *testing.T) {
	h := httptest.NewTLSServer(nil)
	cert := h.TLS.Certificates[0]
	h.Close()
	s := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
	milvuspb.RegisterMilvusServiceServer(s, &server{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(listener)
	defer s.Stop()
	cfg := config.Milvus{Address: listener.Addr().String(), Database: "default", Collection: "test", TLS: true, ConnectTimeout: 200 * time.Millisecond, Timeout: time.Second}
	c, err := client.NewMilvus(context.Background(), cfg)
	if err == nil {
		c.Close()
		t.Fatal("TLS must reject an untrusted certificate")
	}
}
