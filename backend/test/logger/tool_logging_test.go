package logger_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type loggedAlertsFunc func(context.Context) (operations.AlertsResult, error)

func (f loggedAlertsFunc) Alerts(ctx context.Context) (operations.AlertsResult, error) {
	return f(ctx)
}

func TestToolBoundaryPreviewsPreserveResultsAndHideSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, input, requestMarker, responseMarker string
		result                                     operations.AlertsResult
		failure                                    error
		invalidInput                               bool
		forbidden                                  []string
	}{
		{
			name: "small_json", input: "{}", requestMarker: "req[{}]", responseMarker: `"instance":"node-visible"`,
			result: operations.AlertsResult{Source: "fixture", Alerts: []operations.Alert{{Labels: map[string]string{"instance": "node-visible"}}}},
		},
		{
			name: "secret_input", input: `{"accessToken":"input-token","clientSecret":"input-secret","nested":{"X-Api-Key":"input-key"}}`,
			requestMarker: "REDACTED", invalidInput: true, forbidden: []string{"input-token", "input-secret", "input-key"},
		},
		{
			name: "oversized_input", input: `{"password":"` + strings.Repeat("oversized-private-input", 600) + `"}`,
			requestMarker: "req[<truncated>]", invalidInput: true, forbidden: []string{"oversized-private-input"},
		},
		{
			name: "invalid_input", input: `{"password":"invalid-private-input`,
			requestMarker: "req[<invalid>]", invalidInput: true, forbidden: []string{"invalid-private-input"},
		},
		{
			name: "secret_result", input: "{}", requestMarker: "req[{}]", responseMarker: "REDACTED",
			result:    operations.AlertsResult{Source: "fixture", Alerts: []operations.Alert{{Labels: map[string]string{"accessToken": "result-token", "safe": "visible-result"}, Annotations: map[string]string{"client_secret": "result-secret", "X-Api-Key": "result-key"}}}},
			forbidden: []string{"result-token", "result-secret", "result-key"},
		},
		{
			name: "oversized_result", input: "{}", requestMarker: "req[{}]", responseMarker: "rsp[<truncated>]",
			result:    operations.AlertsResult{Source: "fixture", Alerts: []operations.Alert{{Value: strings.Repeat("oversized-private-result", 600)}}},
			forbidden: []string{"oversized-private-result"},
		},
		{
			name: "dependency_failure", input: "{}", requestMarker: "req[{}]",
			failure:   errors.New("private-provider-error https://user:provider-password@fixture/?token=provider-token"),
			forbidden: []string{"private-provider-error", "provider-password", "provider-token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out capture
			cfg := settings()
			cfg.Level = "info"
			ctx, _ := testLogger(t, cfg, logger.Options{Console: &out})
			calls := 0
			registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true}, tools.Dependencies{Prometheus: loggedAlertsFunc(func(context.Context) (operations.AlertsResult, error) {
				calls++
				return tc.result, tc.failure
			})})
			if err != nil {
				t.Fatal(err)
			}
			output, err := registry.Invoke(ctx, tools.QueryPrometheusAlerts, tc.input)
			failed := tc.invalidInput || tc.failure != nil
			if (err != nil) != failed || tc.invalidInput && calls != 0 || !tc.invalidInput && calls != 1 {
				t.Fatal("logging changed tool execution", calls, err)
			}
			text := out.String()
			if strings.Count(text, "开始执行工具") != 1 || !strings.Contains(text, "[INFO]") || !strings.Contains(text, tc.requestMarker) || !strings.Contains(text, fmt.Sprintf("output_bytes[%d]", len(output))) || strings.Count(text, "\n") != 2 {
				t.Fatal("missing tool boundary or byte count", text)
			}
			if failed {
				if output != "" || !strings.Contains(text, "[WARN]") || strings.Count(text, "工具执行失败") != 1 || !strings.Contains(text, "error_type[") || strings.Contains(text, " rsp[") {
					t.Fatal("failure exposed raw output or lost its category", text)
				}
			} else {
				want, _ := json.Marshal(tc.result)
				if output != string(want) || strings.Count(text, "工具执行完成") != 1 || !strings.Contains(text, tc.responseMarker) || !strings.Contains(text, " rsp[") {
					t.Fatal("logging changed or omitted the actual result", text)
				}
				if len(output) <= logger.PreviewLimit && !strings.Contains(text, fmt.Sprintf("count[%d]", len(tc.result.Alerts))) {
					t.Fatal("bounded tool result lost its count", text)
				}
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(text, secret) {
					t.Fatal("secret or unbounded content leaked", secret)
				}
			}
		})
	}
}
