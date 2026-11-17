package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/server"
)

func TestShutdownDrainsAnActiveRequest(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "signal", true: "listener failure"}[failure], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- server.Serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(started)
					select {
					case <-release:
						io.WriteString(w, "completed")
					case <-r.Context().Done():
						t.Error("draining request was cancelled")
					}
				}))
			}()
			response := make(chan error, 1)
			go func() {
				client := &http.Client{Timeout: 2 * time.Second}
				r, err := client.Get("http://" + listener.Addr().String())
				if err == nil {
					defer r.Body.Close()
					var body []byte
					body, err = io.ReadAll(r.Body)
					if err == nil && string(body) != "completed" {
						t.Error("incomplete drained response")
					}
				}
				response <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("request did not reach server")
			}
			if failure {
				listener.Close()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				t.Fatalf("server stopped before request completed: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			if err := <-response; err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if (err != nil) != failure {
					t.Fatal("unexpected shutdown result", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("server did not stop")
			}
		})
	}
}

func TestServeReturnsListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if err := server.Serve(context.Background(), listener, http.NotFoundHandler()); err == nil {
		t.Fatal("listener failure was ignored")
	}
}
