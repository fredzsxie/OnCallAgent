package plan_execute_replan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
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
	Sequence     int         `json:"sequence"`
	Timestamp    string      `json:"timestamp"`
	Iteration    int         `json:"iteration"`
	Agent        string      `json:"agent"`
	Type         string      `json:"type"`
	Tool         string      `json:"tool,omitempty"`
	Status       string      `json:"status"`
	Summary      string      `json:"summary"`
	Count        *int        `json:"count,omitempty"`
	StepID       string      `json:"stepId,omitempty"`
	ParentStepID string      `json:"parentStepId,omitempty"`
	Title        string      `json:"title,omitempty"`
	StartedAt    string      `json:"startedAt,omitempty"`
	FinishedAt   string      `json:"finishedAt,omitempty"`
	DurationMS   *int64      `json:"durationMs,omitempty"`
	Input        *StepInput  `json:"input,omitempty"`
	Output       *StepOutput `json:"output,omitempty"`
	Error        *StepError  `json:"error,omitempty"`
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
