package plan_execute_replan

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func BuildOpsAgent(ctx context.Context, cfg config.OpsAgent, deps Dependencies) (result *Agent, err error) {
	started := time.Now()
	defer func() { logger.Finish(ctx, "Graph", "图编译", started, err, logger.F("graph", "OpsAgent")) }()
	if cfg.Validate() != nil || deps.PlannerModel == nil || deps.ExecutorModel == nil {
		return nil, ErrExecution
	}
	names := []string{}
	for _, t := range deps.Tools {
		if t == nil {
			return nil, ErrExecution
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			return nil, ErrExecution
		}
		names = append(names, info.Name)
	}
	planner, err := NewPlanner(ctx, deps.PlannerModel, strings.Join(names, ", "))
	if err != nil {
		return nil, safeError(ctx, err)
	}
	executor, err := NewExecutor(ctx, deps.ExecutorModel, deps.Tools, cfg.ExecutorMaxSteps)
	if err != nil {
		return nil, safeError(ctx, err)
	}
	replanner, err := NewReplanner(ctx, deps.PlannerModel, strings.Join(names, ", "))
	if err != nil {
		return nil, safeError(ctx, err)
	}
	a, err := planexecute.New(ctx, &planexecute.Config{Planner: planner, Executor: executor, Replanner: replanner, MaxIterations: cfg.MaxIterations})
	if err != nil {
		return nil, safeError(ctx, err)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Agent{a, cfg, deps.Now}, nil
}

func (a *Agent) Run(ctx context.Context, query string) (*Result, error) {
	return a.RunWithEvents(ctx, query, nil)
}

func (a *Agent) RunWithEvents(ctx context.Context, query string, sink func(Event) error) (*Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return &Result{Detail: []Event{}}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	s := &runState{now: a.now, sink: sink, cancel: cancel, events: []Event{}}
	ctx = context.WithValue(ctx, runKey{}, s)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a.agent})
	iter := runner.Query(ctx, query)
	// Next has no context parameter. Drain on a worker while dependencies observe cancellation.
	events := make(chan *adk.AgentEvent)
	go func() {
		defer close(events)
		for {
			e, ok := iter.Next()
			if !ok {
				return
			}
			select {
			case events <- e:
			case <-ctx.Done():
				closeEvent(e)
			}
		}
	}()
	var candidate string
	finished := false
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			break loop
		case e, ok := <-events:
			if !ok {
				break loop
			}
			if e == nil {
				runErr = ErrExecution
				break loop
			}
			if e.Err != nil {
				runErr = e.Err
				closeEvent(e)
				break loop
			}
			msg, _, err := adk.GetMessage(e)
			closeEvent(e)
			if err != nil {
				runErr = err
				break loop
			}
			if msg != nil {
				switch e.AgentName {
				case "Planner":
					var p plan
					if p.UnmarshalJSON([]byte(msg.Content)) != nil {
						runErr = ErrExecution
						break loop
					}
					record(ctx, "Planner", "plan", "", "completed", strings.Join(p.Steps, "; "))
				case "Executor":
					if len(msg.ToolCalls) == 0 && msg.Role == "assistant" {
						record(ctx, "Executor", "execute", "", "completed", "step completed")
					}
				case "Replanner":
					var body map[string]json.RawMessage
					if json.Unmarshal([]byte(msg.Content), &body) != nil {
						runErr = ErrInvalidReport
						break loop
					}
					if _, ok := body["steps"]; ok {
						var p plan
						if p.UnmarshalJSON([]byte(msg.Content)) != nil {
							runErr = ErrExecution
							break loop
						}
						record(ctx, "Replanner", "replan", "", "completed", strings.Join(p.Steps, "; "))
					} else {
						var sections reportSections
						if json.Unmarshal([]byte(msg.Content), &sections) != nil {
							runErr = ErrInvalidReport
							break loop
						}
						candidate, runErr = sections.markdown()
						if runErr != nil {
							break loop
						}
					}
				}
			}
			if e.Action != nil && e.Action.BreakLoop != nil && e.Action.BreakLoop.From == "Replanner" && e.Action.BreakLoop.Done {
				if candidate == "" {
					runErr = ErrInvalidReport
					break loop
				}
				finished = true
			}
		}
	}
	if runErr == nil && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	if runErr == nil && !finished {
		runErr = ErrPlanLimit
	}
	_, sinkErr := s.snapshot()
	if sinkErr != nil {
		runErr = sinkErr
	}
	if runErr != nil {
		runErr = safeError(ctx, runErr)
		if sinkErr != nil {
			runErr = sinkErr
		}
		record(ctx, "Runner", "error", "", "failed", runErr.Error())
		cancel()
	} else {
		record(ctx, "Runner", "complete", "", "completed", "final report accepted")
	}
	detail, sinkErr := s.finish()
	if sinkErr != nil {
		return &Result{Detail: detail}, sinkErr
	}
	if runErr != nil {
		return &Result{Detail: detail}, runErr
	}
	return &Result{Report: candidate, Detail: detail}, nil
}

func closeEvent(e *adk.AgentEvent) {
	if e != nil && e.Output != nil && e.Output.MessageOutput != nil && e.Output.MessageOutput.IsStreaming && e.Output.MessageOutput.MessageStream != nil {
		e.Output.MessageOutput.MessageStream.Close()
	}
}

func ErrorCode(err error) string {
	for _, item := range []struct {
		err  error
		code string
	}{{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}, {ErrInvalidInput, "invalid_input"}, {ErrPlanLimit, "plan_limit"}, {ErrExecutorLimit, "executor_limit"}, {ErrInvalidReport, "invalid_report"}, {ErrEventWrite, "event_write_failed"}} {
		if errors.Is(err, item.err) {
			return item.code
		}
	}
	return "execution_failed"
}
