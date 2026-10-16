package session_test

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"io"
	"strings"
	"testing"
	"time"
)

type agent struct {
	invoke func(context.Context, *pipeline.UserMessage) (*schema.Message, error)
	stream func(context.Context, *pipeline.UserMessage) (*schema.StreamReader[*schema.Message], error)
}

func (a agent) Invoke(ctx context.Context, in *pipeline.UserMessage, _ ...compose.Option) (*schema.Message, error) {
	return a.invoke(ctx, in)
}
func (a agent) Stream(ctx context.Context, in *pipeline.UserMessage, _ ...compose.Option) (*schema.StreamReader[*schema.Message], error) {
	return a.stream(ctx, in)
}
func setup(t *testing.T, a agent, timeout time.Duration) (*chat.ChatService, *mem.Store) {
	t.Helper()
	m, err := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: 30 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := chat.NewChatService(a, m, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return s, m
}
func history(t *testing.T, m *mem.Store, id string) []*schema.Message {
	t.Helper()
	turn, err := m.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Close()
	return turn.History()
}
func callbacks() chat.StreamCallbacks {
	return chat.StreamCallbacks{OnStart: func(context.Context) error { return nil }, OnMessage: func(context.Context, string) error { return nil }, OnComplete: func(context.Context) error { return nil }}
}

func TestSharedHistoryAndStreamCommit(t *testing.T) {
	a := agent{invoke: func(_ context.Context, in *pipeline.UserMessage) (*schema.Message, error) {
		if in.ID != "a" || in.Query != "q" || len(in.History) != 0 {
			t.Fatal("input not normalized")
		}
		return schema.AssistantMessage("first", nil), nil
	}, stream: func(_ context.Context, in *pipeline.UserMessage) (*schema.StreamReader[*schema.Message], error) {
		if len(in.History) != 2 || in.History[1].Role != schema.Assistant {
			t.Fatal("ordinary turn missing from stream history")
		}
		return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant}, {Content: "line1\n"}, {Content: "line2"}}), nil
	}}
	s, m := setup(t, a, time.Second)
	if _, err := s.Chat(context.Background(), " a ", " q "); err != nil {
		t.Fatal(err)
	}
	cb := callbacks()
	var full strings.Builder
	cb.OnMessage = func(_ context.Context, text string) error { full.WriteString(text); return nil }
	cb.OnComplete = func(context.Context) error {
		if len(history(t, m, "other")) != 0 {
			t.Fatal("session leaked")
		}
		return nil
	}
	if err := s.Stream(context.Background(), "a", "next", cb); err != nil {
		t.Fatal(err)
	}
	h := history(t, m, "a")
	if len(h) != 4 || h[3].Content != "line1\nline2" || full.String() != h[3].Content {
		t.Fatal("incomplete stream saved")
	}
}

func TestFailedStreamsNeverCommit(t *testing.T) {
	for _, stage := range []string{"init", "init_with_reader", "receive", "message", "done", "empty", "nil_chunk", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			finished := make(chan struct{})
			a := agent{stream: func(ctx context.Context, _ *pipeline.UserMessage) (*schema.StreamReader[*schema.Message], error) {
				if stage == "init_with_reader" {
					r, w := schema.Pipe[*schema.Message](0)
					go func() {
						defer close(finished)
						defer w.Close()
						w.Send(schema.AssistantMessage("not usable", nil), nil)
					}()
					return r, errors.New("private-secret")
				}
				if stage == "init" {
					close(finished)
					return nil, errors.New("private-secret")
				}
				if stage == "empty" {
					close(finished)
					return schema.StreamReaderFromArray([]*schema.Message{{Content: " "}}), nil
				}
				if stage == "nil_chunk" {
					close(finished)
					return schema.StreamReaderFromArray([]*schema.Message{nil}), nil
				}
				r, w := schema.Pipe[*schema.Message](0)
				go func() {
					defer close(finished)
					defer w.Close()
					if w.Send(schema.AssistantMessage("partial", nil), nil) {
						return
					}
					if stage == "receive" {
						w.Send(nil, errors.New("private-secret"))
					}
					if stage == "cancel" {
						<-ctx.Done()
						w.Send(nil, ctx.Err())
					}
				}()
				return r, nil
			}}
			s, m := setup(t, a, time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cb := callbacks()
			if stage == "message" {
				cb.OnMessage = func(context.Context, string) error { return io.ErrClosedPipe }
			}
			if stage == "done" {
				cb.OnComplete = func(context.Context) error { return io.ErrClosedPipe }
			}
			if stage == "cancel" {
				cb.OnMessage = func(context.Context, string) error { cancel(); return nil }
			}
			err := s.Stream(ctx, "a", "q", cb)
			if err == nil || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("unsafe failure: %v", err)
			}
			if len(history(t, m, "a")) != 0 {
				t.Fatal("failed stream saved a partial turn")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("stream not closed")
			}
		})
	}
}

func TestSameIdSerializationDifferentIdsAndWaitingCancel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a := agent{invoke: func(ctx context.Context, in *pipeline.UserMessage) (*schema.Message, error) {
		if in.Query == "first" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if in.Query == "second" && len(in.History) != 2 {
			t.Error("second turn did not observe completed first")
		}
		return schema.AssistantMessage(in.Query, nil), nil
	}}
	s, m := setup(t, a, time.Second)
	done := make(chan error, 1)
	go func() { _, err := s.Chat(context.Background(), "a", "first"); done <- err }()
	<-entered
	if _, err := s.Chat(context.Background(), "b", "parallel"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Chat(ctx, "a", "waiting"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting did not cancel")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Chat(context.Background(), "a", "second"); err != nil {
		t.Fatal(err)
	}
	if len(history(t, m, "a")) != 4 {
		t.Fatal("cancelled waiter saved history")
	}
}

func TestInvokeFailureAndBudget(t *testing.T) {
	for _, mode := range []string{"error", "empty", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			a := agent{invoke: func(ctx context.Context, _ *pipeline.UserMessage) (*schema.Message, error) {
				switch mode {
				case "error":
					return nil, errors.New("private-secret")
				case "empty":
					return schema.AssistantMessage(" ", nil), nil
				default:
					<-ctx.Done()
					return nil, ctx.Err()
				}
			}}
			s, m := setup(t, a, 20*time.Millisecond)
			_, err := s.Chat(context.Background(), "a", "q")
			if err == nil || strings.Contains(err.Error(), "private-secret") {
				t.Fatal("unsafe error")
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("timeout lost")
			}
			if len(history(t, m, "a")) != 0 {
				t.Fatal("failed invoke stored history")
			}
		})
	}
}
