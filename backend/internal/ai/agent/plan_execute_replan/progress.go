package plan_execute_replan

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type StepInput struct {
	Query string `json:"query,omitempty"`
}
type StepError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type AlertDetail struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    string            `json:"activeAt"`
	Value       string            `json:"value"`
}
type DocumentDetail struct {
	Title       string  `json:"title"`
	Source      string  `json:"source"`
	SectionPath string  `json:"sectionPath,omitempty"`
	Version     string  `json:"version,omitempty"`
	Score       float64 `json:"score"`
	ScoreType   string  `json:"scoreType,omitempty"`
	Excerpt     string  `json:"excerpt"`
	Truncated   bool    `json:"truncated,omitempty"`
}
type ToolSummary struct {
	Tool    string `json:"tool"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Count   *int   `json:"count,omitempty"`
}
type StepOutput struct {
	Steps       []string         `json:"steps,omitempty"`
	Timestamp   string           `json:"timestamp,omitempty"`
	TimeZone    string           `json:"timezone,omitempty"`
	UnixSeconds *int64           `json:"unix_seconds,omitempty"`
	Source      string           `json:"source,omitempty"`
	Query       string           `json:"query,omitempty"`
	Mode        string           `json:"mode,omitempty"`
	ScoreType   string           `json:"scoreType,omitempty"`
	Alerts      []AlertDetail    `json:"alerts,omitempty"`
	Documents   []DocumentDetail `json:"documents,omitempty"`
	Tools       []ToolSummary    `json:"tools,omitempty"`
	Truncated   bool             `json:"truncated,omitempty"`
}

// CloneEvents makes the callback, HTTP response and stored history independent.
func CloneEvents(events []Event) []Event {
	out := make([]Event, len(events))
	for i, e := range events {
		out[i] = e
		if e.Count != nil {
			n := *e.Count
			out[i].Count = &n
		}
		if e.DurationMS != nil {
			n := *e.DurationMS
			out[i].DurationMS = &n
		}
		if e.Input != nil {
			v := *e.Input
			out[i].Input = &v
		}
		if e.Error != nil {
			v := *e.Error
			out[i].Error = &v
		}
		if e.Output != nil {
			v := *e.Output
			v.Steps = append([]string(nil), v.Steps...)
			if v.UnixSeconds != nil {
				n := *v.UnixSeconds
				v.UnixSeconds = &n
			}
			v.Alerts = append([]AlertDetail(nil), v.Alerts...)
			for j := range v.Alerts {
				v.Alerts[j].Labels = copyFields(v.Alerts[j].Labels)
				v.Alerts[j].Annotations = copyFields(v.Alerts[j].Annotations)
			}
			v.Documents = append([]DocumentDetail(nil), v.Documents...)
			v.Tools = append([]ToolSummary(nil), v.Tools...)
			for j := range v.Tools {
				if v.Tools[j].Count != nil {
					n := *v.Tools[j].Count
					v.Tools[j].Count = &n
				}
			}
			out[i].Output = &v
		}
	}
	return out
}

func copyFields(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type runKey struct{}
type activeStep struct {
	event   Event
	started time.Time
}
type runState struct {
	mu                                 sync.Mutex
	iteration                          int
	events                             []Event
	now                                func() time.Time
	sink                               func(Event) error
	err                                error
	cancel                             context.CancelFunc
	closed                             bool
	nextID                             int
	active                             map[string]activeStep
	plannerID, executorID, replannerID string
}

func state(ctx context.Context) *runState { s, _ := ctx.Value(runKey{}).(*runState); return s }

// The lock serializes parallel tool callbacks and preserves sequence on the wire.
// Network sinks must bound their writes; a failed sink cancels the whole run.
func (s *runState) emit(ctx context.Context, e Event) {
	if s.closed || s.err != nil {
		return
	}
	e.Sequence = len(s.events) + 1
	e.Timestamp = s.now().Format(time.RFC3339Nano)
	e.Summary = clip(e.Summary, 1024)
	s.events = append(s.events, CloneEvents([]Event{e})[0])
	fields := []logger.Field{logger.F("agent", e.Agent), logger.F("event", e.Type), logger.F("iteration", e.Iteration), logger.F("sequence", e.Sequence), logger.F("tool", e.Tool), logger.F("status", e.Status), logger.F("step_id", e.StepID)}
	if e.Count != nil {
		fields = append(fields, logger.F("count", *e.Count))
	}
	if e.DurationMS != nil {
		fields = append(fields, logger.F("duration_ms", *e.DurationMS))
	}
	log := logger.Info
	if e.Status == "failed" {
		log = logger.Warn
	}
	log(ctx, "Ops", "运维执行事件", fields...)
	if s.sink != nil && s.sink(CloneEvents([]Event{e})[0]) != nil {
		s.err = ErrEventWrite
		s.cancel()
	}
}
func (s *runState) start(ctx context.Context, agent, kind, tool, parent, title string, input *StepInput) string {
	if s.closed || s.err != nil || ctx.Err() != nil {
		return ""
	}
	if s.active == nil {
		s.active = make(map[string]activeStep)
	}
	s.nextID++
	id := fmt.Sprintf("step-%d", s.nextID)
	now := s.now()
	e := Event{StepID: id, ParentStepID: parent, Title: clip(title, 1024), Agent: agent, Type: kind, Tool: tool, Iteration: s.iteration, Status: "started", Summary: clip(title, 1024), StartedAt: now.Format(time.RFC3339Nano), Input: input}
	s.active[id] = activeStep{e, now}
	s.emit(ctx, e)
	return id
}
func (s *runState) end(ctx context.Context, id, status, summary string, output *StepOutput, failure *StepError, count *int) {
	step, ok := s.active[id]
	if !ok {
		return
	}
	delete(s.active, id)
	e := step.event
	now := s.now()
	elapsed := max(int64(0), now.Sub(step.started).Milliseconds())
	e.Status, e.Summary, e.Output, e.Error, e.Count = status, summary, output, failure, count
	e.FinishedAt, e.DurationMS = now.Format(time.RFC3339Nano), &elapsed
	s.emit(ctx, e)
}
func beginPlanning(ctx context.Context) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.plannerID = s.start(ctx, "Planner", "plan", "", "", "制定排查计划", nil)
	}
}
func beginExecution(ctx context.Context, steps []string) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		output := &StepOutput{Steps: append([]string(nil), steps...)}
		if s.iteration == 0 {
			s.end(ctx, s.plannerID, "completed", fmt.Sprintf("已制定 %d 个排查步骤", len(steps)), output, nil, nil)
		} else {
			s.end(ctx, s.replannerID, "completed", fmt.Sprintf("已调整计划，后续 %d 个步骤", len(steps)), output, nil, nil)
		}
		s.iteration++
		title := "执行排查步骤"
		if len(steps) > 0 {
			title = steps[0]
		}
		s.executorID = s.start(ctx, "Executor", "execute", "", "", title, nil)
	}
}
func beginReview(ctx context.Context) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		tools := []ToolSummary{}
		failed := 0
		for _, e := range s.events {
			if e.ParentStepID == s.executorID && e.Type == "tool" && e.Status != "started" {
				tools = append(tools, ToolSummary{e.Tool, e.Status, e.Summary, e.Count})
				if e.Status != "completed" {
					failed++
				}
			}
		}
		summary := "本步骤已完成，未调用外部工具"
		if len(tools) > 0 {
			summary = fmt.Sprintf("已执行 %d 项工具调用：成功 %d 项，失败 %d 项", len(tools), len(tools)-failed, failed)
		}
		s.end(ctx, s.executorID, "completed", summary, &StepOutput{Tools: tools}, nil, nil)
		s.replannerID = s.start(ctx, "Replanner", "replan", "", "", "复核证据与调整计划", nil)
	}
}
func startTool(ctx context.Context, name string, input *StepInput) string {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.start(ctx, "Executor", "tool", name, s.executorID, toolTitle(name), input)
	}
	return ""
}
func endTool(ctx context.Context, id, status, summary string, output *StepOutput, failure *StepError, count *int) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.end(ctx, id, status, summary, output, failure, count)
	}
}
func finishProgress(ctx context.Context, err error) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err == nil {
			s.end(ctx, s.replannerID, "completed", "证据复核完成，已生成并校验分析报告", nil, nil, nil)
		}
		// Close children before parents, in creation order, even on cancellation.
		for i := s.nextID; i > 0; i-- {
			id := fmt.Sprintf("step-%d", i)
			if _, ok := s.active[id]; ok {
				status := "failed"
				if ctx.Err() == context.Canceled {
					status = "cancelled"
				}
				s.end(ctx, id, status, "分析中断："+ErrorCode(err), nil, &StepError{Code: ErrorCode(err), Message: "本步骤未完成", Retryable: false}, nil)
			}
		}
		status, kind, summary := "completed", "complete", "分析报告已通过校验"
		if err != nil {
			status, kind, summary = "failed", "error", err.Error()
		}
		s.emit(ctx, Event{Agent: "Runner", Type: kind, Status: status, Summary: summary, Iteration: s.iteration})
	}
}
func (s *runState) snapshot() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return CloneEvents(s.events), s.err
}
func (s *runState) finish() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return CloneEvents(s.events), s.err
}
