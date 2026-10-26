package ops_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func TestOpsLogsSummariesWithoutPlanOrReportContents(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "tool-failed"}[fail], func(t *testing.T) {
			var out bytes.Buffer
			logs, err := logger.New(config.Logging{Level: "info", Dir: "./log", RetentionDays: 7}, logger.Options{Console: &out})
			if err != nil {
				t.Fatal(err)
			}
			defer logs.Close()
			ctx, id := logger.WithRequest(logger.WithLogger(context.Background(), logs))
			a := build(t, cfg(), plannerModel(), toolExecutor(t, false, fail), registry(t, false, fail))
			result, err := service(t, a, time.Second).Analyze(ctx)
			if err != nil || result.Report == "" {
				t.Fatal(err)
			}
			for _, want := range []string{"req=" + id, "event=plan", "event=execute", "event=tool", "tool=query_prometheus_alerts", "运维分析完成", "query=" + strconv.QuoteToGraphic(aiops.DefaultQuery)} {
				if !strings.Contains(out.String(), want) {
					t.Fatal("missing event", want, out.String())
				}
			}
			for _, secret := range []string{"Inspect alerts and evidence", "auth.md", "HighCPU on node-a", "fixture-private", "stale key cache"} {
				if strings.Contains(out.String(), secret) {
					t.Fatal("plan/report leaked", secret)
				}
			}
		})
	}
}

func TestOpsHTTPLogsFixedEnvelope(t *testing.T) {
	dir := t.TempDir()
	logs, err := logger.New(config.Logging{Level: "info", Dir: dir, FileEnabled: true, RetentionDays: 7}, logger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	root := logger.WithLogger(context.Background(), logs)
	a := build(t, cfg(), plannerModel(), toolExecutor(t, false, false), registry(t, false, false))
	url := startOpsAPI(t, service(t, a, time.Second), root)
	for _, body := range []string{"{}", `{"invalid":"private-invalid-body"}`} {
		response, err := http.Post(url+"/api/ai_ops", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	files, _ := filepath.Glob(filepath.Join(dir, "oncall-*.log"))
	if len(files) != 1 {
		t.Fatal("missing log")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "request={}") != 1 || strings.Count(text, "query="+strconv.QuoteToGraphic(aiops.DefaultQuery)) != 1 || strings.Contains(text, "private-invalid-body") {
		t.Fatal("invalid or duplicate request content", text)
	}
}

func TestOpsCLILogsOriginalTask(t *testing.T) {
	upstream, _, _ := modelServer(t, "normal")
	defer upstream.Close()
	query := "  排查任务\n\"quoted\"\t  "
	cmd := exec.Command(opsBinary, "--query", query)
	cmd.Dir = t.TempDir()
	cmd.Env = cleanEnv(modelEnv(upstream.URL)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 {
		t.Fatal(err, stderr.String())
	}
	files, _ := filepath.Glob(filepath.Join(cmd.Dir, "log", "oncall-*.log"))
	if len(files) != 1 {
		t.Fatal("missing log")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "query="+strconv.QuoteToGraphic(query)) != 1 || strings.Contains(string(data), "planner-key") {
		t.Fatal("original task missing or credentials logged", string(data))
	}
}
