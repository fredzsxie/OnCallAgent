package logger_test

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controller "github.com/fredzsxie/OnCallAgent/backend/internal/controller/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/health"
	"github.com/fredzsxie/OnCallAgent/backend/internal/controller/upload"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
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
			server := ghttp.GetServer("route-table-" + guid.S())
			middleware.ObserveHTTP(server, root)
			server.Group("/api", func(group *ghttp.RouterGroup) {
				group.Middleware(middleware.ResponseMiddleware)
				group.Bind(controller.NewV1(nil, nil))
				group.Bind(controller.NewAIOpsV1(nil))
				group.Bind(health.NewV1())
				group.Group("", func(files *ghttp.RouterGroup) {
					files.Middleware(middleware.UploadBody(1024))
					files.Bind(upload.NewV1(nil))
				})
			})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			if err := server.SetListener(listener); err != nil {
				listener.Close()
				t.Fatal(err)
			}
			if err := server.Start(); err != nil {
				listener.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := server.Shutdown(); err != nil {
					t.Error(err)
				}
			})
			middleware.PrintRoutes(root, server)
			output := console.String()
			if level == "warn" {
				if output != "" {
					t.Fatal("INFO route table ignored level", output)
				}
				return
			}
			for _, want := range []string{"已注册 API 路由", "count=5", "ADDRESS", "METHOD", "ROUTE", "HANDLER", "MIDDLEWARE", "POST", "GET", "/api/ai_ops", "/api/chat", "/api/chat_stream", "/api/health", "/api/upload", "ResponseMiddleware", "UploadBody", "AIOpsController", "ControllerV1"} {
				if !strings.Contains(output, want) {
					t.Fatalf("missing %q in %s", want, output)
				}
			}
			if strings.Count(output, "(utility/middleware/routes.go:") != 1 {
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
			response, err := http.Get("http://" + listener.Addr().String() + "/api/health")
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
