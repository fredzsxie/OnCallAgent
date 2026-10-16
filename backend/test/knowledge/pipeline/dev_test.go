package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/eino-ext/devops"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

func TestDevHelper(t *testing.T) {
	port := os.Getenv("ONCALL_PIPELINE_DEV_HELPER")
	if port == "" {
		t.Skip("subprocess helper")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := devops.Init(ctx, devops.WithDevServerPort(port)); err != nil {
		t.Fatal(err)
	}
	build(t, settings(t), &testutil.MemoryStore{})
	<-ctx.Done()
}

func TestDevDiscoveryAndSignals(t *testing.T) {
	for _, stopSignal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(stopSignal.String(), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			logfile, err := os.Create(filepath.Join(t.TempDir(), "dev.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer logfile.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestDevHelper$", "-test.timeout=20s")
			cmd.Env = append(os.Environ(), fmt.Sprintf("ONCALL_PIPELINE_DEV_HELPER=%d", port))
			cmd.Stdout, cmd.Stderr = logfile, logfile
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			base := fmt.Sprintf("http://127.0.0.1:%d/eino/devops/debug/v1/graphs", port)
			client := &http.Client{Timeout: time.Second}
			graphID := ""
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				resp, err := client.Get(base)
				if err == nil {
					var body struct {
						Data struct{ Graphs []struct{ ID, Name string } }
					}
					decodeErr := json.NewDecoder(resp.Body).Decode(&body)
					resp.Body.Close()
					if decodeErr == nil && resp.StatusCode == 200 {
						for _, graph := range body.Data.Graphs {
							if graph.Name == "KnowledgeIndexing" {
								graphID = graph.ID
							}
						}
					}
				}
				if graphID != "" {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("dev exited before graph discovery: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
			}
			if graphID == "" {
				t.Fatal("KnowledgeIndexing was not discoverable")
			}
			resp, err := client.Get(base + "/" + graphID + "/canvas")
			if err != nil {
				t.Fatal(err)
			}
			var canvas struct {
				Data struct {
					Canvas struct {
						Name  string
						Nodes []json.RawMessage
						Edges []json.RawMessage
					} `json:"canvas_info"`
				}
			}
			err = json.NewDecoder(resp.Body).Decode(&canvas)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || canvas.Data.Canvas.Name != "KnowledgeIndexing" || len(canvas.Data.Canvas.Nodes) != 5 || len(canvas.Data.Canvas.Edges) != 4 {
				t.Fatalf("invalid runtime canvas: %v %+v", err, canvas)
			}
			select {
			case err := <-done:
				t.Fatalf("dev failed to stay alive: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			if err := cmd.Process.Signal(stopSignal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("signal exit:", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("dev failed to exit after signal")
			}
		})
	}
}
