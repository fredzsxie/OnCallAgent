package sse

import (
	"context"
	"errors"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
)

type Service struct{ chat *chat.ChatService }

func New(service *chat.ChatService) (*Service, error) {
	if service == nil {
		return nil, chat.ErrInitialization
	}
	return &Service{chat: service}, nil
}

func (s *Service) Serve(ctx context.Context, r *ghttp.Request, id, question string) error {
	started := time.Now()
	var executionErr error
	chunks, contentBytes := 0, 0
	defer func() {
		middleware.SetExecutionError(r, executionErr)
		logger.Finish(ctx, "SSE", "流式响应", started, executionErr, logger.F("chunks", chunks), logger.F("content_bytes", contentBytes))
	}()
	var writer *streamWriter
	defer func() {
		if writer != nil {
			writer.close()
		}
	}()
	err := s.chat.Stream(ctx, id, question, chat.StreamCallbacks{
		OnStart: func(ctx context.Context) error {
			var err error
			writer, err = newWriter(r)
			if err != nil {
				return err
			}
			err = writer.send(ctx, "connected", map[string]string{"status": "connected"})
			if err == nil {
				logger.Info(ctx, "SSE", "连接已建立")
			}
			return err
		},
		OnMessage: func(ctx context.Context, content string) error {
			err := writer.send(ctx, "message", map[string]string{"content": content})
			if err == nil {
				chunks++
				contentBytes += len(content)
			}
			return err
		},
		OnComplete: func(ctx context.Context) error {
			return writer.send(ctx, "done", map[string]string{"status": "completed"})
		},
	})
	executionErr = err
	if writer == nil || !writer.started {
		return err
	}
	// Never append an event to a partial transport frame or after a terminal event.
	if err != nil && !writer.broken && !writer.terminal && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		_, body := middleware.ErrorResponse(err)
		_ = writer.send(ctx, "error", map[string]string{"message": body.Message})
	}
	return nil
}
