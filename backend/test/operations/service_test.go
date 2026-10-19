package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

type searchFunc func(context.Context, operations.LogRequest) ([]json.RawMessage, error)

func (f searchFunc) Search(ctx context.Context, r operations.LogRequest) ([]json.RawMessage, error) {
	return f(ctx, r)
}
func logConfig() config.LogMCP {
	return config.LogMCP{Command: "fixture-mcp", Args: []string{"--stdio"}, Timeout: 15 * time.Second, SecretID: "fixture-id", SecretKey: "fixture-key", TimeZone: "Asia/Shanghai", Region: "ap-guangzhou", TopicID: "fixture-topic", Lookback: 15 * time.Minute, Limit: 20}
}

func TestLogDefaultsOverridesAndTimeConversion(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 123000000, time.FixedZone("CST", 8*3600))
	var actual operations.LogRequest
	clockCalls := 0
	s, err := operations.NewLogService(searchFunc(func(ctx context.Context, r operations.LogRequest) ([]json.RawMessage, error) {
		actual = r
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 15*time.Second {
			t.Error("missing tool budget")
		}
		return []json.RawMessage{}, nil
	}), logConfig(), func() time.Time { clockCalls++; return now })
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Query(context.Background(), &operations.LogInput{Query: " level:ERROR "})
	if err != nil || actual.From != now.Add(-15*time.Minute).UnixMilli() || actual.To != now.UnixMilli() || actual.Limit != 20 || actual.Region != "ap-guangzhou" || actual.Query != "level:ERROR" || clockCalls != 1 {
		t.Fatalf("default request %+v %v", actual, err)
	}
	if string(mustJSON(t, r)) == "null" || r.Records == nil {
		t.Fatal("empty records must be array")
	}
	limit := 5
	_, err = s.Query(context.Background(), &operations.LogInput{Query: "* | SELECT count(*)", Region: "ap-shanghai", TopicID: "override", StartTime: "2026-10-19T12:00:00.123+08:00", EndTime: "2026-10-19T04:01:00.456Z", Limit: &limit})
	if err != nil || actual.To-actual.From != 60333 || actual.Region != "ap-shanghai" || actual.TopicID != "override" || actual.Limit != 5 {
		t.Fatalf("override %+v %v", actual, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLogInvalidInputNeverCallsDependency(t *testing.T) {
	calls := 0
	s, _ := operations.NewLogService(searchFunc(func(context.Context, operations.LogRequest) ([]json.RawMessage, error) { calls++; return nil, nil }), logConfig(), nil)
	zero, large := 0, 1001
	for _, input := range []*operations.LogInput{nil, {}, {Query: " "}, {Query: "*", Limit: &zero}, {Query: "*", Limit: &large}, {Query: "*", StartTime: "2026-10-19T00:00:00Z"}, {Query: "*", StartTime: "2026-10-19T00:00:00", EndTime: "2026-10-19T00:10:00Z"}, {Query: "*", StartTime: "2026-10-19T01:00:00Z", EndTime: "2026-10-19T00:00:00Z"}} {
		if _, err := s.Query(context.Background(), input); !errors.Is(err, operations.ErrInvalidInput) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached MCP")
	}
}

func TestLogCancellationParentBudgetAndSafeErrors(t *testing.T) {
	s, _ := operations.NewLogService(searchFunc(func(ctx context.Context, _ operations.LogRequest) ([]json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), logConfig(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Query(ctx, &operations.LogInput{Query: "*"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := s.Query(ctx, &operations.LogInput{Query: "*"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, _ = operations.NewLogService(searchFunc(func(context.Context, operations.LogRequest) ([]json.RawMessage, error) {
		return nil, errors.New("fixture-private-secret")
	}), logConfig(), nil)
	_, err := s.Query(context.Background(), &operations.LogInput{Query: "*"})
	if !errors.Is(err, operations.ErrUpstream) || err.Error() != "query_log: upstream request failed" {
		t.Fatal("unsafe error", err)
	}
}
