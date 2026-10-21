package sse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

type streamWriter struct {
	request  *ghttp.Request
	writer   http.ResponseWriter
	control  *http.ResponseController
	started  bool
	broken   bool
	terminal bool
}

func newWriter(r *ghttp.Request) (*streamWriter, error) {
	if r == nil || r.Response == nil {
		return nil, chat.ErrInitialization
	}
	// GoFrame's buffered Flush discards I/O errors; write through its tracked raw writer instead.
	base := r.Response.Writer.ResponseWriter
	_, flush := base.(http.Flusher)
	_, flushError := base.(interface{ FlushError() error })
	control := http.NewResponseController(base)
	if (!flush && !flushError) || control.SetWriteDeadline(time.Time{}) != nil {
		return nil, chat.ErrInitialization
	}
	return &streamWriter{request: r, writer: r.Response.RawWriter(), control: control}, nil
}

func (w *streamWriter) send(ctx context.Context, event string, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := w.control.SetWriteDeadline(deadline); err != nil {
		w.broken = true
		return err
	}
	if !w.started {
		w.request.Response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.request.Response.Header().Set("Cache-Control", "no-cache")
		w.request.Response.Header().Set("X-Accel-Buffering", "no")
		w.request.Response.WriteHeader(http.StatusOK)
		middleware.MarkStreaming(w.request)
		w.started = true
		w.writer.WriteHeader(http.StatusOK)
	}
	frame := []byte("event: " + event + "\ndata: " + string(encoded) + "\n\n")
	n, err := w.writer.Write(frame)
	if err != nil {
		w.broken = true
		return err
	}
	if n != len(frame) {
		w.broken = true
		return io.ErrShortWrite
	}
	if event == "done" || event == "error" {
		w.terminal = true
	}
	if err := w.control.Flush(); err != nil {
		w.broken = true
		return err
	}
	return ctx.Err()
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
			_ = writer.control.SetWriteDeadline(time.Time{})
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
