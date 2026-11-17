package sse

import (
	"context"
	"net/http"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
)

// ServeOps shares framing, flush/error handling and deadlines with chat SSE.
func ServeOps(ctx context.Context, w http.ResponseWriter, service *aiops.AIOpsService) error {
	started := time.Now()
	var executionErr error
	defer func() {
		middleware.SetExecutionError(ctx, executionErr)
		logger.Finish(ctx, "SSE", "运维进度流", started, executionErr)
	}()
	writer, err := newWriter(w)
	if err != nil {
		executionErr = aiops.Wrap(aiops.ErrInitialization, nil)
		return executionErr
	}
	defer writer.close()
	if err = writer.send(ctx, "connected", map[string]string{"status": "connected"}); err != nil {
		executionErr = aiops.Wrap(pipeline.ErrEventWrite, nil)
		if !writer.started {
			return executionErr
		}
		return nil
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop, stopped := make(chan struct{}), make(chan struct{})
	heartbeatFailure := make(chan error, 1)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				if err := writer.comment(streamCtx); err != nil {
					heartbeatFailure <- err
					cancel()
					return
				}
			}
		}
	}()
	out, err := service.RunWithEvents(streamCtx, aiops.DefaultQuery, func(event pipeline.Event) error { return writer.send(streamCtx, "progress", event) })
	close(stop)
	<-stopped
	select {
	case <-heartbeatFailure:
		err = aiops.Wrap(pipeline.ErrEventWrite, out.Detail)
	default:
	}
	if err != nil {
		executionErr = err
		if !writer.broken && !writer.terminal && ctx.Err() == nil {
			_, body := middleware.ErrorResponse(err)
			if sendErr := writer.send(ctx, "error", body); sendErr != nil {
				executionErr = aiops.Wrap(pipeline.ErrEventWrite, out.Detail)
			}
		}
		return nil
	}
	if err = writer.send(ctx, "done", struct {
		Status string           `json:"status"`
		Result string           `json:"result"`
		Detail []pipeline.Event `json:"detail"`
	}{"completed", out.Report, out.Detail}); err != nil {
		executionErr = aiops.Wrap(pipeline.ErrEventWrite, out.Detail)
	}
	return nil
}
