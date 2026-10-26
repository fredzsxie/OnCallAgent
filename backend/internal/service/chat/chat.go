package chat

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
)

var ErrInvalidInput = errors.New("Id and Question must not be empty")

var ErrInitialization = errors.New("chat service initialization failed")

type Agent interface {
	Invoke(context.Context, *pipeline.UserMessage, ...compose.Option) (*schema.Message, error)
	Stream(context.Context, *pipeline.UserMessage, ...compose.Option) (*schema.StreamReader[*schema.Message], error)
}

type ChatService struct {
	agent   Agent
	memory  *mem.Store
	timeout time.Duration
}

type StreamCallbacks struct {
	OnStart    func(context.Context) error
	OnMessage  func(context.Context, string) error
	OnComplete func(context.Context) error
}

func NewChatService(agent Agent, memory *mem.Store, timeout time.Duration) (*ChatService, error) {
	if agent == nil || memory == nil || timeout <= 0 {
		return nil, ErrInitialization
	}
	return &ChatService{agent: agent, memory: memory, timeout: timeout}, nil
}

func normalize(id, question string) (string, string, error) {
	id, question = strings.TrimSpace(id), strings.TrimSpace(question)
	if id == "" || question == "" {
		return "", "", ErrInvalidInput
	}
	return id, question, nil
}

func (s *ChatService) Chat(ctx context.Context, id, question string) (answer string, err error) {
	started := time.Now()
	committed := false
	defer func() {
		logger.Finish(ctx, "Chat", "对话执行", started, err, logger.F("mode", "invoke"), logger.F("history_committed", committed))
	}()
	id, question, err = normalize(id, question)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	waiting := time.Now()
	turn, err := s.memory.Acquire(ctx, id)
	if err != nil {
		return "", executionError(ctx, err)
	}
	defer turn.Close()
	logger.Info(ctx, "Chat", "开始对话", logger.F("session_hash", logger.Digest(id)), logger.F("lock_wait", time.Since(waiting)), logger.F("history_rounds", len(turn.History())/2))
	out, err := s.agent.Invoke(ctx, &pipeline.UserMessage{ID: id, Query: question, History: turn.History()})
	if err != nil {
		return "", executionError(ctx, err)
	}
	if ctx.Err() != nil {
		return "", executionError(ctx, ctx.Err())
	}
	if out == nil || out.Role != schema.Assistant || len(out.ToolCalls) != 0 || strings.TrimSpace(out.Content) == "" {
		return "", pipeline.ErrAgentFailed
	}
	turn.Commit(question, out.Content)
	committed = true
	return out.Content, nil
}

func (s *ChatService) Stream(ctx context.Context, id, question string, callbacks StreamCallbacks) (err error) {
	started := time.Now()
	committed := false
	defer func() {
		logger.Finish(ctx, "Chat", "对话执行", started, err, logger.F("mode", "stream"), logger.F("history_committed", committed))
	}()
	id, question, err = normalize(id, question)
	if err != nil {
		return err
	}
	if callbacks.OnStart == nil || callbacks.OnMessage == nil || callbacks.OnComplete == nil {
		return ErrInitialization
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return executionError(ctx, err)
	}
	if err := callbacks.OnStart(ctx); err != nil {
		return err
	}
	waiting := time.Now()
	turn, err := s.memory.Acquire(ctx, id)
	if err != nil {
		return executionError(ctx, err)
	}
	defer turn.Close()
	logger.Info(ctx, "Chat", "开始对话", logger.F("session_hash", logger.Digest(id)), logger.F("lock_wait", time.Since(waiting)), logger.F("history_rounds", len(turn.History())/2))
	reader, err := s.agent.Stream(ctx, &pipeline.UserMessage{ID: id, Query: question, History: turn.History()})
	if reader != nil {
		defer reader.Close()
	}
	if err != nil {
		return executionError(ctx, err)
	}
	if reader == nil {
		return pipeline.ErrAgentFailed
	}
	var answer strings.Builder
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return executionError(ctx, err)
		}
		if ctx.Err() != nil {
			return executionError(ctx, ctx.Err())
		}
		if chunk == nil || len(chunk.ToolCalls) != 0 || (chunk.Role != "" && chunk.Role != schema.Assistant) {
			return pipeline.ErrAgentFailed
		}
		if chunk.Content == "" {
			continue
		}
		if err := callbacks.OnMessage(ctx, chunk.Content); err != nil {
			return err
		}
		answer.WriteString(chunk.Content)
	}
	if ctx.Err() != nil {
		return executionError(ctx, ctx.Err())
	}
	if strings.TrimSpace(answer.String()) == "" {
		return pipeline.ErrAgentFailed
	}
	// Publish a complete pair only after the completion callback has also succeeded.
	if err := callbacks.OnComplete(ctx); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return executionError(ctx, ctx.Err())
	}
	turn.Commit(question, answer.String())
	committed = true
	return nil
}

func executionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, compose.ErrExceedMaxSteps} {
		if errors.Is(err, cause) {
			return errors.Join(pipeline.ErrAgentFailed, cause)
		}
	}
	return pipeline.ErrAgentFailed
}
