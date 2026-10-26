package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

type alertQuery func(context.Context) (operations.AlertsResult, error)

func (f alertQuery) Alerts(ctx context.Context) (operations.AlertsResult, error) { return f(ctx) }

func TestOperationsToolsInvokeAndStreamInActualGraph(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, name := range []string{tools.QueryPrometheusAlerts} {
			t.Run(fmt.Sprintf("%s/stream=%t", name, stream), func(t *testing.T) {
				calls := 0
				registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true}, tools.Dependencies{
					Prometheus: alertQuery(func(ctx context.Context) (operations.AlertsResult, error) {
						calls++
						return operations.AlertsResult{Source: "prometheus", Alerts: []operations.Alert{{Labels: map[string]string{"instance": "host-a"}}}}, nil
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				model := &scriptedModel{generate: func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
					last := in[len(in)-1]
					if last.Role != schema.Tool {
						args := "{}"
						return toolCall(name, args), nil
					}
					var result map[string]any
					if json.Unmarshal([]byte(last.Content), &result) != nil || !strings.Contains(last.Content, "host-a") {
						t.Error("tool result double encoded or lost fields")
					}
					return schema.AssistantMessage("verified operations", nil), nil
				}}
				graph := build(t, settings(), pipeline.Dependencies{Model: model, Retriever: retrieverFunc(documents), Tools: registry.Tools()})
				var answer string
				if stream {
					reader, err := graph.Stream(context.Background(), &pipeline.UserMessage{Query: "inspect"})
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					for {
						chunk, err := reader.Recv()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						answer += chunk.Content
					}
				} else {
					result, err := graph.Invoke(context.Background(), &pipeline.UserMessage{Query: "inspect"})
					if err != nil {
						t.Fatal(err)
					}
					answer = result.Content
				}
				if answer != "verified operations" || calls != 1 {
					t.Fatalf("answer=%q calls=%d", answer, calls)
				}
			})
		}
	}
}

func TestOperationsToolFailureStopsAgent(t *testing.T) {
	registry, err := tools.NewRegistry(config.Tools{TimeZone: "UTC", QueryPrometheusAlertsEnabled: true}, tools.Dependencies{Prometheus: alertQuery(func(context.Context) (operations.AlertsResult, error) {
		return operations.AlertsResult{}, operations.Failure(tools.QueryPrometheusAlerts, operations.ErrAuthentication)
	})})
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		return toolCall(tools.QueryPrometheusAlerts, "{}"), nil
	}}
	graph := build(t, settings(), pipeline.Dependencies{Model: model, Retriever: retrieverFunc(documents), Tools: registry.Tools()})
	if _, err := graph.Invoke(context.Background(), &pipeline.UserMessage{Query: "inspect"}); !errors.Is(err, pipeline.ErrAgentFailed) {
		t.Fatal("tool error did not stop graph", err)
	}
}
