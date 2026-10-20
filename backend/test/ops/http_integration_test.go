package ops_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
)

func TestAIOpsHTTPIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 and ONCALL_AIOPS_INTEGRATION_URL for real read-only HTTP analysis")
	}
	base := os.Getenv("ONCALL_AIOPS_INTEGRATION_URL")
	if base == "" {
		t.Skip("ONCALL_AIOPS_INTEGRATION_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/api/ai_ops", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("real HTTP analysis request failed")
	}
	defer r.Body.Close()
	var body struct {
		Message string `json:"message"`
		Data    struct {
			Result string           `json:"result"`
			Detail []pipeline.Event `json:"detail"`
			Code   string           `json:"code"`
		} `json:"data"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		t.Fatal("invalid real HTTP response")
	}
	if r.StatusCode != 200 {
		t.Fatalf("real analysis did not complete: HTTP=%d safe_code=%s partial_events=%d", r.StatusCode, body.Data.Code, len(body.Data.Detail))
	}
	if body.Message != "OK" || body.Data.Result == "" || body.Data.Detail == nil {
		t.Fatal("missing final report/detail")
	}
	for _, e := range body.Data.Detail {
		if e.Type == "tool" {
			if e.Count == nil {
				t.Logf("tool=%s status=%s count=unavailable", e.Tool, e.Status)
			} else {
				t.Logf("tool=%s status=%s count=%d", e.Tool, e.Status, *e.Count)
			}
		}
	}
	t.Logf("real HTTP report returned; events=%d; full report and business data not logged", len(body.Data.Detail))
}
