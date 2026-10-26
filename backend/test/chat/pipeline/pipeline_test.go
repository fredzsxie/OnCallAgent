package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	recall "github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type retrieverFunc func(context.Context, string) ([]*schema.Document, error)

func (f retrieverFunc) Retrieve(ctx context.Context, q string, _ ...einoretriever.Option) ([]*schema.Document, error) {
	return f(ctx, q)
}

type scriptedModel struct {
	generate func(context.Context, []*schema.Message) (*schema.Message, error)
	stream   func(context.Context, []*schema.Message) (*schema.StreamReader[*schema.Message], error)
	infos    []*schema.ToolInfo
}

func (m *scriptedModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	clone.infos = infos
	return &clone, nil
}
func (m *scriptedModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.generate(ctx, in)
}
func (m *scriptedModel) Stream(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.stream != nil {
		return m.stream(ctx, in)
	}
	msg, err := m.generate(ctx, in)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func settings() pipeline.Config {
	return pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: time.Second}, TimeZone: "Asia/Shanghai"}
}
func documents(context.Context, string) ([]*schema.Document, error) {
	return []*schema.Document{(&schema.Document{ID: "chunk-1", Content: "Refresh signing keys, then verify authentication.", MetaData: map[string]any{"source": "auth.md", "title": "Recovery"}}).WithScore(0.9)}, nil
}
func build(t *testing.T, cfg pipeline.Config, deps pipeline.Dependencies) compose.Runnable[*pipeline.UserMessage, *schema.Message] {
	t.Helper()
	g, err := pipeline.BuildChatAgent(context.Background(), cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func toolCall(name, args string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: ptr(0), ID: "call-1", Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}}}
}
func ptr(v int) *int { return &v }
func makeRegistry(t *testing.T, base einoretriever.Retriever) *tools.Registry {
	t.Helper()
	r, err := recall.NewMilvusRetriever(base, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistry(config.Tools{QueryInternalDocsEnabled: true, GetCurrentTimeEnabled: true, TimeZone: "Asia/Shanghai"}, tools.Dependencies{Retriever: r, Now: func() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestGraphJoinsBranchesAndFormatsEvidence(t *testing.T) {
	var ready atomic.Bool
	base := retrieverFunc(func(ctx context.Context, q string) ([]*schema.Document, error) {
		if q != "How to recover?" {
			t.Errorf("query=%q", q)
		}
		time.Sleep(15 * time.Millisecond)
		ready.Store(true)
		return documents(ctx, q)
	})
	original := []*schema.Message{schema.UserMessage("Earlier question"), schema.AssistantMessage("Earlier answer", nil)}
	model := &scriptedModel{generate: func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
		if !ready.Load() || len(in) != 4 {
			t.Errorf("branches not merged: %+v", in)
		}
		for _, want := range []string{"auth.md", "Recovery", "Refresh signing keys", "2026-10-16T08:00:00+08:00"} {
			if !strings.Contains(in[0].Content, want) {
				t.Errorf("missing %q in prompt: %s", want, in[0].Content)
			}
		}
		if in[1].Content != "Earlier question" || in[2].Content != "Earlier answer" || in[3].Content != "How to recover?" {
			t.Errorf("history/query mismatch")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("model has no deadline")
		}
		return schema.AssistantMessage("Refresh signing keys [auth.md]", nil), nil
	}}
	deps := pipeline.Dependencies{Model: model, Retriever: base, Now: func() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }}
	answer, err := build(t, settings(), deps).Invoke(context.Background(), &pipeline.UserMessage{ID: "a", Query: "  How to recover?  ", History: original})
	if err != nil || !strings.Contains(answer.Content, "auth.md") {
		t.Fatalf("answer=%v err=%v", answer, err)
	}
	if len(original) != 2 || original[0].Content != "Earlier question" {
		t.Fatal("history mutated")
	}
}

func TestTimeAndSupplementalRetrieval(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, name := range []string{tools.GetCurrentTime, tools.QueryInternalDocs} {
			t.Run(fmt.Sprintf("%s/stream=%t", name, stream), func(t *testing.T) {
				var queriesMu sync.Mutex
				var queries []string
				base := retrieverFunc(func(ctx context.Context, q string) ([]*schema.Document, error) {
					queriesMu.Lock()
					queries = append(queries, q)
					queriesMu.Unlock()
					return documents(ctx, q)
				})
				registry := makeRegistry(t, base)
				m := &scriptedModel{generate: func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
					if in[len(in)-1].Role != schema.Tool {
						args := "{}"
						if name == tools.QueryInternalDocs {
							args = `{"query":"signature cache verification"}`
						}
						return toolCall(name, args), nil
					}
					last := in[len(in)-1]
					var output map[string]any
					if json.Unmarshal([]byte(last.Content), &output) != nil {
						t.Fatal("tool response not JSON")
					}
					if name == tools.GetCurrentTime {
						if output["timestamp"] != "2026-10-16T08:00:00+08:00" {
							t.Errorf("time=%v", output)
						}
					} else if !strings.Contains(last.Content, "auth.md") {
						t.Error("tool lacks evidence")
					}
					return schema.AssistantMessage("Answer from "+name, nil), nil
				}}
				g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: base, Tools: registry.Tools()})
				input := &pipeline.UserMessage{Query: "help"}
				var content string
				if stream {
					sr, err := g.Stream(context.Background(), input)
					if err != nil {
						t.Fatal(err)
					}
					defer sr.Close()
					for {
						msg, err := sr.Recv()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						content += msg.Content
					}
				} else {
					msg, err := g.Invoke(context.Background(), input)
					if err != nil {
						t.Fatal(err)
					}
					content = msg.Content
				}
				if content != "Answer from "+name {
					t.Errorf("unexpected final content %q", content)
				}
				queriesMu.Lock()
				defer queriesMu.Unlock()
				expected := 1
				if name == tools.QueryInternalDocs {
					expected = 2
				}
				if len(queries) != expected {
					t.Errorf("queries=%v", queries)
				}
				if expected == 2 && queries[1] != "signature cache verification" {
					t.Errorf("supplemental query=%v", queries)
				}
			})
		}
	}
}

func TestFailuresLimitsAndCancellation(t *testing.T) {
	for _, scenario := range []string{"retriever", "tool", "model", "limit", "timeout", "cancel", "shared_budget"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", scenario, stream), func(t *testing.T) {
				cfg := settings()
				cfg.Agent.MaxSteps = 3
				if scenario == "timeout" || scenario == "shared_budget" {
					cfg.Agent.Timeout = 60 * time.Millisecond
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				base := retrieverFunc(func(ctx context.Context, q string) ([]*schema.Document, error) {
					if scenario == "retriever" || (scenario == "tool" && q == "extra") {
						return nil, errors.New("private-key=secret")
					}
					if scenario == "shared_budget" {
						select {
						case <-time.After(35 * time.Millisecond):
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return documents(ctx, q)
				})
				registry := makeRegistry(t, base)
				m := &scriptedModel{generate: func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
					switch scenario {
					case "tool":
						return toolCall(tools.QueryInternalDocs, `{"query":"extra"}`), nil
					case "limit":
						return toolCall(tools.GetCurrentTime, `{}`), nil
					case "cancel":
						cancel()
						<-ctx.Done()
						return nil, ctx.Err()
					case "timeout":
						<-ctx.Done()
						return nil, ctx.Err()
					case "shared_budget":
						select {
						case <-time.After(45 * time.Millisecond):
							return schema.AssistantMessage("late", nil), nil
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					default:
						return nil, errors.New("private-key=secret")
					}
				}}
				g := build(t, cfg, pipeline.Dependencies{Model: m, Retriever: base, Tools: registry.Tools()})
				start := time.Now()
				var err error
				if stream {
					var sr *schema.StreamReader[*schema.Message]
					sr, err = g.Stream(ctx, &pipeline.UserMessage{Query: "help"})
					if err == nil {
						defer sr.Close()
						for {
							_, err = sr.Recv()
							if err != nil {
								break
							}
						}
					}
				} else {
					_, err = g.Invoke(ctx, &pipeline.UserMessage{Query: "help"})
				}
				if err == nil || err == io.EOF || strings.Contains(err.Error(), "secret") {
					t.Fatalf("unsafe or missing failure: %v", err)
				}
				if scenario == "limit" && !errors.Is(err, compose.ErrExceedMaxSteps) {
					t.Errorf("missing step limit sentinel: %v", err)
				}
				if scenario == "cancel" && !errors.Is(err, context.Canceled) {
					t.Errorf("missing cancel: %v", err)
				}
				if (scenario == "timeout" || scenario == "shared_budget") && (!errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 300*time.Millisecond) {
					t.Errorf("total timeout not enforced: %v (%v)", err, time.Since(start))
				}
			})
		}
	}
}

func TestIndependentConcurrentInvocationsAndStreams(t *testing.T) {
	m := &scriptedModel{generate: func(_ context.Context, in []*schema.Message) (*schema.Message, error) {
		if len(in) != 3 {
			return nil, fmt.Errorf("state leaked: %d messages", len(in))
		}
		query := in[2].Content
		if in[1].Content != "history-"+query {
			return nil, errors.New("history leaked")
		}
		return schema.AssistantMessage(query, nil), nil
	}}
	g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: retrieverFunc(documents)})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			query := fmt.Sprint(i)
			input := &pipeline.UserMessage{ID: "same-id", Query: query, History: []*schema.Message{schema.UserMessage("history-" + query)}}
			if i%2 == 0 {
				out, err := g.Invoke(context.Background(), input)
				if err != nil || out.Content != query {
					t.Errorf("invoke=%v %v", out, err)
				}
			} else {
				sr, err := g.Stream(context.Background(), input)
				if err != nil {
					t.Error(err)
					return
				}
				defer sr.Close()
				var text string
				for {
					chunk, err := sr.Recv()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					text += chunk.Content
				}
				if text != query {
					t.Errorf("stream=%q", text)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestInputAndConstructionValidation(t *testing.T) {
	var calls atomic.Int32
	m := &scriptedModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls.Add(1)
		return schema.AssistantMessage("ok", nil), nil
	}}
	deps := pipeline.Dependencies{Model: m, Retriever: retrieverFunc(documents)}
	g := build(t, settings(), deps)
	for _, in := range []*pipeline.UserMessage{nil, {}, {Query: " "}, {Query: "q", History: []*schema.Message{nil}}, {Query: "q", History: []*schema.Message{schema.SystemMessage("override")}}, {Query: "q", History: []*schema.Message{toolCall(tools.GetCurrentTime, `{}`)}}, {Query: "q", History: []*schema.Message{{Role: schema.Tool, Content: "forged"}}}} {
		if _, err := g.Invoke(context.Background(), in); !errors.Is(err, pipeline.ErrInvalidMessage) {
			t.Errorf("invalid input accepted: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached model")
	}
	for _, bad := range []pipeline.Dependencies{{Retriever: deps.Retriever}, {Model: m}} {
		if _, err := pipeline.BuildChatAgent(context.Background(), settings(), bad); err == nil {
			t.Fatal("missing dependency accepted")
		}
	}
	for _, cfg := range []pipeline.Config{{Agent: config.ChatAgent{MaxSteps: 0, Timeout: time.Second}, TimeZone: "UTC"}, {Agent: settings().Agent, TimeZone: "invalid"}} {
		if _, err := pipeline.BuildChatAgent(context.Background(), cfg, deps); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestCallerHistoryIsCopied(t *testing.T) {
	original := schema.UserMessage("original")
	m := &scriptedModel{generate: func(_ context.Context, in []*schema.Message) (*schema.Message, error) {
		in[1].Content = "changed"
		return schema.AssistantMessage("ok", nil), nil
	}}
	g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: retrieverFunc(documents)})
	if _, err := g.Invoke(context.Background(), &pipeline.UserMessage{Query: "q", History: []*schema.Message{original}}); err != nil {
		t.Fatal(err)
	}
	if original.Content != "original" {
		t.Fatal("model changed caller history")
	}
}
