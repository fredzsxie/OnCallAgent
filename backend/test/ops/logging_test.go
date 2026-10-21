package ops_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
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
			for _, want := range []string{"req=" + id, "event=plan", "event=execute", "event=tool", "tool=query_prometheus_alerts", "运维分析完成"} {
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
