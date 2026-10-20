package aiops

import (
	"context"
	"errors"
	"strings"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
)

const DefaultQuery = `检查当前 Prometheus 告警，保留每个告警实例，核对当前时间并检索相关内部排查手册。若日志工具可用，使用已配置或明确提供的地域和主题查询相关日志，不猜测查询范围。根据实际证据生成包含告警、证据、根因候选、处理建议及未确认事项的中文报告，引用文档来源。无告警不等于系统健康；明确说明禁用、失败或缺失的工具和证据。仅分析，不执行修复。`

var ErrInitialization = errors.New("ops service initialization failed")
var ErrInternal = errors.New("ops internal error")

type Agent interface {
	RunWithEvents(context.Context, string, func(pipeline.Event) error) (*pipeline.Result, error)
}

type AIOpsService struct {
	agent   Agent
	timeout time.Duration
}

type Error struct {
	Code    string
	Message string
	Detail  []pipeline.Event
	cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

func New(agent Agent, timeout time.Duration) (*AIOpsService, error) {
	if agent == nil || timeout <= 0 {
		return nil, ErrInitialization
	}
	return &AIOpsService{agent, timeout}, nil
}

func (s *AIOpsService) Analyze(ctx context.Context) (*pipeline.Result, error) {
	return s.RunWithEvents(ctx, DefaultQuery, nil)
}

func (s *AIOpsService) RunWithEvents(ctx context.Context, query string, sink func(pipeline.Event) error) (*pipeline.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return &pipeline.Result{Detail: []pipeline.Event{}}, Wrap(pipeline.ErrInvalidInput, nil)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var out *pipeline.Result
	err := ctx.Err()
	if err == nil {
		out, err = s.agent.RunWithEvents(ctx, query, sink)
	}
	result := &pipeline.Result{Detail: []pipeline.Event{}}
	if out != nil {
		result.Report = out.Report
		result.Detail = cloneEvents(out.Detail)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && strings.TrimSpace(result.Report) == "" {
		err = pipeline.ErrInvalidReport
	}
	if err != nil {
		result.Report = ""
		return result, Wrap(err, result.Detail)
	}
	return result, nil
}

// Expose only known categories; retain partial evidence without leaking provider errors.
func Wrap(err error, detail []pipeline.Event) *Error {
	cause := error(pipeline.ErrExecution)
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, pipeline.ErrInvalidInput, pipeline.ErrPlanLimit, pipeline.ErrExecutorLimit, pipeline.ErrInvalidReport, pipeline.ErrEventWrite, ErrInitialization, ErrInternal} {
		if errors.Is(err, known) {
			cause = known
			break
		}
	}
	code := pipeline.ErrorCode(cause)
	if errors.Is(cause, ErrInitialization) {
		code = "initialization_failed"
	}
	if errors.Is(cause, ErrInternal) {
		code = "internal_error"
	}
	return &Error{code, cause.Error(), cloneEvents(detail), cause}
}

func cloneEvents(events []pipeline.Event) []pipeline.Event {
	result := append([]pipeline.Event{}, events...)
	for i := range result {
		if result[i].Count != nil {
			count := *result[i].Count
			result[i].Count = &count
		}
	}
	return result
}
