package plan_execute_replan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

var (
	ErrInvalidInput  = errors.New("invalid ops query")
	ErrExecution     = errors.New("ops agent execution failed")
	ErrPlanLimit     = errors.New("ops plan iteration limit reached")
	ErrExecutorLimit = errors.New("ops executor step limit reached")
	ErrInvalidReport = errors.New("ops agent returned no valid final report")
	ErrEventWrite    = errors.New("could not write ops events")
)

type Dependencies struct {
	PlannerModel  model.ToolCallingChatModel
	ExecutorModel model.ToolCallingChatModel
	Tools         []tool.BaseTool
	Now           func() time.Time
}

type Event struct {
	Sequence  int    `json:"sequence"`
	Timestamp string `json:"timestamp"`
	Iteration int    `json:"iteration"`
	Agent     string `json:"agent"`
	Type      string `json:"type"`
	Tool      string `json:"tool,omitempty"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Count     *int   `json:"count,omitempty"`
}

type Result struct {
	Report string  `json:"report"`
	Detail []Event `json:"detail"`
}

type Agent struct {
	agent adk.Agent
	cfg   config.OpsAgent
	now   func() time.Time
}

type runKey struct{}
type runState struct {
	mu        sync.Mutex
	iteration int
	events    []Event
	now       func() time.Time
	sink      func(Event) error
	err       error
	cancel    context.CancelFunc
	closed    bool
}

func state(ctx context.Context) *runState { s, _ := ctx.Value(runKey{}).(*runState); return s }

func record(ctx context.Context, agent, kind, toolName, status, summary string, counts ...int) {
	s := state(ctx)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil || s.closed {
		return
	}
	runes := []rune(summary)
	if len(runes) > 1024 {
		summary = string(runes[:1024]) + "…"
	}
	e := Event{Sequence: len(s.events) + 1, Timestamp: s.now().Format(time.RFC3339Nano), Iteration: s.iteration, Agent: agent, Type: kind, Tool: toolName, Status: status, Summary: summary}
	if len(counts) > 0 {
		count := counts[0]
		e.Count = &count
	}
	s.events = append(s.events, e)
	fields := []logger.Field{logger.F("agent", agent), logger.F("event", kind), logger.F("iteration", s.iteration), logger.F("sequence", e.Sequence), logger.F("tool", toolName), logger.F("status", status)}
	if e.Count != nil {
		fields = append(fields, logger.F("count", *e.Count))
	}
	log := logger.Info
	if status == "failed" {
		log = logger.Warn
	}
	log(ctx, "Ops", "运维执行事件", fields...)
	if s.sink != nil && s.sink(e) != nil {
		s.err = ErrEventWrite
		s.cancel()
	}
}

func (s *runState) snapshot() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event{}, s.events...), s.err
}

func (s *runState) finish() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A cancelled SDK call may unwind after Run returns; no callback may write to a closed sink.
	s.closed = true
	return append([]Event{}, s.events...), s.err
}

func safeError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrInvalidInput, ErrPlanLimit, ErrExecutorLimit, ErrInvalidReport, ErrEventWrite} {
		if errors.Is(err, category) {
			return category
		}
	}
	if errors.Is(err, adk.ErrExceedMaxIterations) || errors.Is(err, compose.ErrExceedMaxSteps) {
		return ErrExecutorLimit
	}
	return ErrExecution
}

type reportSections struct {
	Alerts          string `json:"alerts" jsonschema:"required"`
	Evidence        string `json:"evidence" jsonschema:"required"`
	RootCauses      string `json:"root_causes" jsonschema:"required"`
	Recommendations string `json:"recommendations" jsonschema:"required"`
	Unconfirmed     string `json:"unconfirmed" jsonschema:"required"`
}

func (r reportSections) markdown() (string, error) {
	var out strings.Builder
	for _, section := range []struct{ title, body string }{{"告警", r.Alerts}, {"证据", r.Evidence}, {"根因候选", r.RootCauses}, {"处理建议", r.Recommendations}, {"未确认事项", r.Unconfirmed}} {
		if strings.TrimSpace(section.body) == "" {
			return "", ErrInvalidReport
		}
		fmt.Fprintf(&out, "## %s\n\n%s\n\n", section.title, strings.TrimSpace(section.body))
	}
	return strings.TrimSpace(out.String()), nil
}
