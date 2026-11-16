package sse

import (
	"context"
	"encoding/json"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gogf/gf/v2/net/ghttp"
	"io"
	"net/http"
	"sync"
	"time"
)

type streamWriter struct {
	mu       sync.Mutex
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken || w.terminal {
		return io.ErrClosedPipe
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return w.writeFrame(ctx, []byte("event: "+event+"\ndata: "+string(encoded)+"\n\n"), event == "done" || event == "error")
}

func (w *streamWriter) comment(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken || w.terminal {
		return io.ErrClosedPipe
	}
	return w.writeFrame(ctx, []byte(": heartbeat\n\n"), false)
}

func (w *streamWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.control.SetWriteDeadline(time.Time{})
}

func (w *streamWriter) writeFrame(ctx context.Context, frame []byte, terminal bool) error {
	if err := ctx.Err(); err != nil {
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
	n, err := w.writer.Write(frame)
	if err != nil {
		w.broken = true
		return err
	}
	if n != len(frame) {
		w.broken = true
		return io.ErrShortWrite
	}
	if terminal {
		w.terminal = true
	}
	if err := w.control.Flush(); err != nil {
		w.broken = true
		return err
	}
	return ctx.Err()
}
