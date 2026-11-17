package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

// Serve drains active requests before the caller closes shared application clients.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	s := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	result := make(chan error, 1)
	go func() { result <- s.Serve(listener) }()
	var runErr error
	running := true
	select {
	case runErr = <-result:
		running = false
	case <-ctx.Done():
	}
	logger.Info(ctx, "HTTP", "正在停止 HTTP 服务")
	// The signal context is already cancelled; draining needs its own bounded deadline.
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdown); err != nil {
		// Close cancels outstanding requests before application dependencies are released.
		_ = s.Close()
		if running {
			runErr = <-result
		}
		return errors.Join(serveError(runErr), err)
	}
	if running {
		runErr = <-result
	}
	return serveError(runErr)
}

func serveError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
