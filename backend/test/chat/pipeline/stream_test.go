package pipeline_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
)

func TestStreamFragmentedToolCallsAndFinalChunks(t *testing.T) {
	registry := makeRegistry(t, retrieverFunc(documents))
	var calls atomic.Int32
	m := &scriptedModel{stream: func(_ context.Context, in []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		calls.Add(1)
		if in[len(in)-1].Role != schema.Tool {
			return schema.StreamReaderFromArray([]*schema.Message{
				{Role: schema.Assistant, Content: " \n"},
				{Content: "I will search the documentation first."},
				{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: ptr(0), ID: "split-call", Type: "function", Function: schema.FunctionCall{Name: tools.QueryInternalDocs, Arguments: `{"query":"signature`}}}},
				{ToolCalls: []schema.ToolCall{{Index: ptr(0), Function: schema.FunctionCall{Arguments: ` cache"}`}}}},
			}), nil
		}
		if !strings.Contains(in[len(in)-1].Content, "auth.md") {
			t.Error("tool arguments not assembled")
		}
		return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: "Refresh "}, {Content: "keys [auth.md]"}}), nil
	}}
	g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: retrieverFunc(documents), Tools: registry.Tools()})
	sr, err := g.Stream(context.Background(), &pipeline.UserMessage{Query: "help"})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	var chunks []string
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(msg.ToolCalls) != 0 || msg.Role == schema.Tool {
			t.Fatal("internal tool messages leaked")
		}
		chunks = append(chunks, msg.Content)
	}
	if strings.Join(chunks, "") != "Refresh keys [auth.md]" || len(chunks) < 2 || calls.Load() != 2 {
		t.Fatalf("chunks=%v calls=%d", chunks, calls.Load())
	}
}

func TestStreamCancellationAndBackpressure(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting_for_model", true: "slow_consumer"}[blocked], func(t *testing.T) {
			finished := make(chan struct{})
			started := make(chan struct{})
			m := &scriptedModel{stream: func(ctx context.Context, _ []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
				r, w := schema.Pipe[*schema.Message](0)
				go func() {
					defer close(finished)
					defer w.Close()
					close(started)
					if blocked {
						for i := 0; i < 20; i++ {
							if w.Send(schema.AssistantMessage("part", nil), nil) {
								return
							}
						}
						return
					}
					if w.Send(schema.AssistantMessage("first", nil), nil) {
						return
					}
					<-ctx.Done()
					w.Send(nil, ctx.Err())
				}()
				return r, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: retrieverFunc(documents)})
			if !blocked {
				go func() { <-started; cancel() }()
			}
			sr, err := g.Stream(ctx, &pipeline.UserMessage{Query: "help"})
			if err != nil {
				if blocked || !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				if blocked {
					if _, err = sr.Recv(); err != nil {
						t.Fatal(err)
					}
					cancel()
				} else {
					_, err = sr.Recv()
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel lost: %v", err)
					}
				}
				sr.Close()
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("stream producer leaked after cancel and close")
			}
		})
	}
}

func TestEmptyEvidenceAndDisabledTool(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		m := &scriptedModel{generate: func(_ context.Context, in []*schema.Message) (*schema.Message, error) {
			if strings.Contains(in[0].Content, "Refresh signing keys") {
				t.Error("unexpected evidence")
			}
			if disabled {
				return toolCall(tools.GetCurrentTime, `{}`), nil
			}
			return schema.AssistantMessage("No supporting documentation is available.", nil), nil
		}}
		g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: retrieverFunc(func(context.Context, string) ([]*schema.Document, error) { return nil, nil })})
		msg, err := g.Invoke(context.Background(), &pipeline.UserMessage{Query: "help"})
		if disabled {
			if !errors.Is(err, pipeline.ErrAgentFailed) {
				t.Fatal("disabled tool did not abort")
			}
		} else if err != nil || !strings.Contains(msg.Content, "No supporting") {
			t.Fatal("empty retrieval did not reach model")
		}
	}
}

func TestEarlierParentDeadline(t *testing.T) {
	var modelCalls atomic.Int32
	m := &scriptedModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		modelCalls.Add(1)
		return schema.AssistantMessage("late", nil), nil
	}}
	r := retrieverFunc(func(ctx context.Context, _ string) ([]*schema.Document, error) { <-ctx.Done(); return nil, ctx.Err() })
	g := build(t, settings(), pipeline.Dependencies{Model: m, Retriever: r})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := g.Invoke(ctx, &pipeline.UserMessage{Query: "help"})
	if !errors.Is(err, context.DeadlineExceeded) || modelCalls.Load() != 0 {
		t.Fatalf("deadline=%v calls=%d", err, modelCalls.Load())
	}
}
