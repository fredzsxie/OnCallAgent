package ops_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
)

type opsFrame struct {
	name string
	data json.RawMessage
}

func nextOpsFrame(t *testing.T, r *bufio.Reader) opsFrame {
	t.Helper()
	f := opsFrame{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return f
		}
		if strings.HasPrefix(line, "event: ") {
			f.name = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			f.data = json.RawMessage(strings.TrimPrefix(line, "data: "))
		}
		if strings.HasPrefix(line, ":") {
			f.name = "comment"
		}
	}
}
func postOpsStream(t *testing.T, url, body string) *http.Response {
	t.Helper()
	r, err := http.Post(url+"/api/ai_ops_stream", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpsSSEProgressArrivesBeforeCompletionAndFinalSnapshotMatches(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	e := pipeline.Event{Sequence: 1, Timestamp: time.Now().Format(time.RFC3339Nano), Iteration: 1, Agent: "Executor", Type: "execute", Status: "started", Summary: "collect evidence", StepID: "step-1"}
	s := service(t, runFunc(func(ctx context.Context, _ string, sink func(pipeline.Event) error) (*pipeline.Result, error) {
		if err := sink(e); err != nil {
			return nil, err
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &pipeline.Result{Report: "valid report", Detail: []pipeline.Event{e}}, nil
	}), time.Second)
	r := postOpsStream(t, startOpsAPI(t, s), "{}")
	defer r.Body.Close()
	if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") || r.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("missing streaming response")
	}
	reader := bufio.NewReader(r.Body)
	if f := nextOpsFrame(t, reader); f.name != "connected" {
		t.Fatal(f)
	}
	f := nextOpsFrame(t, reader)
	if f.name != "progress" {
		t.Fatal(f)
	}
	var got pipeline.Event
	if json.Unmarshal(f.data, &got) != nil || !reflect.DeepEqual(got, e) {
		t.Fatal("wrong progress")
	}
	release <- struct{}{}
	f = nextOpsFrame(t, reader)
	var done struct {
		Status, Result string
		Detail         []pipeline.Event
	}
	if f.name != "done" || json.Unmarshal(f.data, &done) != nil || done.Status != "completed" || done.Result != "valid report" || !reflect.DeepEqual(done.Detail, []pipeline.Event{e}) {
		t.Fatal("wrong final snapshot", f)
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) > 0 {
		t.Fatal("extra data after terminal", string(rest), err)
	}
}

func TestOpsSSEValidationFailureAndTimeout(t *testing.T) {
	a := build(t, cfg(), plannerModel(), toolExecutor(t, false, false), registry(t, false, false))
	url := startOpsAPI(t, service(t, a, time.Second))
	for _, body := range []string{`{"query":"no"}`, `null`, `[]`, `{broken`} {
		r := postOpsStream(t, url, body)
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 400 || !json.Valid(data) || strings.Contains(string(data), "event:") {
			t.Fatal("invalid body became a stream", string(data))
		}
	}
	for _, cause := range []error{context.DeadlineExceeded, errors.New("private-provider-error")} {
		t.Run(cause.Error(), func(t *testing.T) {
			s := service(t, runFunc(func(context.Context, string, func(pipeline.Event) error) (*pipeline.Result, error) {
				return &pipeline.Result{Detail: []pipeline.Event{{Sequence: 1, Summary: "partial"}}}, cause
			}), time.Second)
			r := postOpsStream(t, startOpsAPI(t, s), "{}")
			defer r.Body.Close()
			reader := bufio.NewReader(r.Body)
			nextOpsFrame(t, reader)
			f := nextOpsFrame(t, reader)
			if f.name != "error" || strings.Contains(string(f.data), "private-provider") {
				t.Fatal("unsafe error frame", string(f.data))
			}
			var data struct {
				Data struct {
					Code   string
					Detail []pipeline.Event
				}
			}
			if json.Unmarshal(f.data, &data) != nil || len(data.Data.Detail) != 1 || data.Data.Code == "" {
				t.Fatal("lost failure contract")
			}
		})
	}
}

func TestOpsSSEHeartbeatAndDisconnectCancelUpstream(t *testing.T) {
	finished := make(chan struct{})
	s := service(t, runFunc(func(ctx context.Context, _ string, _ func(pipeline.Event) error) (*pipeline.Result, error) {
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}), 12*time.Second)
	r := postOpsStream(t, startOpsAPI(t, s), "{}")
	reader := bufio.NewReader(r.Body)
	if nextOpsFrame(t, reader).name != "connected" || nextOpsFrame(t, reader).name != "comment" {
		t.Fatal("missing heartbeat")
	}
	r.Body.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("upstream survived disconnect")
	}
}

type opsWriteFailure struct {
	http.ResponseWriter
	stage     string
	failFlush bool
}

func (w *opsWriteFailure) SetWriteDeadline(d time.Time) error {
	if w.stage == "init" {
		return http.ErrNotSupported
	}
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(d)
}
func (w *opsWriteFailure) Write(data []byte) (int, error) {
	if strings.HasPrefix(string(data), "event: "+w.stage+"\n") {
		return 0, io.ErrClosedPipe
	}
	if w.stage == "done_flush" && strings.HasPrefix(string(data), "event: done\n") {
		w.failFlush = true
	}
	return w.ResponseWriter.Write(data)
}
func (w *opsWriteFailure) FlushError() error {
	if w.failFlush {
		return io.ErrClosedPipe
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func TestOpsSSEWriteFailuresDoNotAppendTerminalFrames(t *testing.T) {
	for _, stage := range []string{"init", "connected", "progress", "done", "done_flush"} {
		t.Run(stage, func(t *testing.T) {
			a := build(t, cfg(), plannerModel(), toolExecutor(t, false, false), registry(t, false, false))
			url := startOpsAPIWriter(t, service(t, a, time.Second), func(w http.ResponseWriter) http.ResponseWriter {
				return &opsWriteFailure{ResponseWriter: w, stage: stage}
			})
			r := postOpsStream(t, url, "{}")
			data, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if stage == "init" {
				if r.StatusCode != 500 || !json.Valid(data) {
					t.Fatal("bad initialization error")
				}
				return
			}
			if strings.Contains(string(data), "event: error") || (stage != "done_flush" && strings.Contains(string(data), "event: done")) {
				t.Fatal("appended terminal after broken frame")
			}
		})
	}
}
