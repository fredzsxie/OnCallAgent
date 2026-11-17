package logger_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/router"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
)

func TestStartupRouteTablePreservesAPIInformation(t *testing.T) {
	for _, level := range []string{"info", "warn"} {
		t.Run(level, func(t *testing.T) {
			cfg := settings()
			cfg.Level = level
			cfg.Dir = t.TempDir()
			cfg.FileEnabled = true
			var console capture
			root, _ := testLogger(t, cfg, logger.Options{Console: &console})
			engine := router.New(root, router.Dependencies{MaxUploadBytes: 1024})
			server := httptest.NewServer(engine)
			t.Cleanup(server.Close)
			middleware.PrintRoutes(root, engine)
			output := console.String()
			if level == "warn" {
				if output != "" {
					t.Fatal("INFO route table ignored level", output)
				}
				return
			}
			for _, want := range []string{"已注册 API 路由", "count[12]", "METHOD", "ROUTE", "HANDLER", "POST", "GET", "PUT", "/api/ai_ops", "/api/ai_ops_stream", "/api/chat", "/api/chat_stream", "/api/health", "/api/upload", "/api/knowledge/documents", "/api/knowledge/documents/:id", "/api/knowledge/documents/:id/chunks", "/api/knowledge/search", "AIOpsController", "ControllerV1"} {
				if !strings.Contains(output, want) {
					t.Fatalf("missing %q in %s", want, output)
				}
			}
			if strings.Count(output, "utility/middleware/routes.go:") != 1 {
				t.Fatal("route caller must appear only on table heading", output)
			}
			for _, skip := range []string{"HookBeforeServe", "HookAfterOutput", "ObserveHTTP", "/*"} {
				if strings.Contains(output, skip) {
					t.Fatal("internal tracing hook exposed as API", skip, output)
				}
			}
			logs, err := filepath.Glob(filepath.Join(cfg.Dir, "oncall-*.log"))
			if err != nil || len(logs) != 1 {
				t.Fatal(logs, err)
			}
			file, err := os.ReadFile(logs[0])
			if err != nil || string(file) != output {
				t.Fatal("terminal/file route tables differ", err)
			}
			// Printing the table must not change response envelopes or request tracing.
			response, err := http.Get(server.URL + "/api/health")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || response.Header.Get("X-Request-ID") == "" || !strings.Contains(string(body), `"message":"OK"`) {
				t.Fatal("HTTP regression", err, string(body))
			}
		})
	}
}
