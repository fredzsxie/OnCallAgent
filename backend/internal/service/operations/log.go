package operations

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type LogService struct {
	client LogSearcher
	cfg    config.LogMCP
	now    func() time.Time
}

func NewLogService(client LogSearcher, cfg config.LogMCP, now func() time.Time) (*LogService, error) {
	if client == nil || cfg.Validate() != nil {
		return nil, Failure(LogTool, ErrConfiguration)
	}
	if now == nil {
		now = time.Now
	}
	return &LogService{client, cfg, now}, nil
}

func (s *LogService) Query(ctx context.Context, input *LogInput) (LogResult, error) {
	if input == nil {
		return LogResult{}, Failure(LogTool, ErrInvalidInput)
	}
	request := LogRequest{Query: strings.TrimSpace(input.Query), Region: s.cfg.Region, TopicID: s.cfg.TopicID, Limit: s.cfg.Limit}
	if input.Region != "" {
		request.Region = strings.TrimSpace(input.Region)
	}
	if input.TopicID != "" {
		request.TopicID = strings.TrimSpace(input.TopicID)
	}
	if input.Limit != nil {
		request.Limit = *input.Limit
	}
	if request.Query == "" || len(request.Query) > 12*1024 || request.Region == "" || request.TopicID == "" || request.Limit < 1 || request.Limit > 1000 {
		return LogResult{}, Failure(LogTool, ErrInvalidInput)
	}
	end := s.now()
	start := end.Add(-s.cfg.Lookback)
	if input.StartTime != "" || input.EndTime != "" {
		var err error
		if start, err = time.Parse(time.RFC3339Nano, input.StartTime); err != nil {
			return LogResult{}, Failure(LogTool, ErrInvalidInput)
		}
		if end, err = time.Parse(time.RFC3339Nano, input.EndTime); err != nil {
			return LogResult{}, Failure(LogTool, ErrInvalidInput)
		}
	}
	request.From, request.To = start.UnixMilli(), end.UnixMilli()
	if request.From < 0 || request.From >= request.To {
		return LogResult{}, Failure(LogTool, ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.HTTP.Timeout)
	defer cancel()
	records, err := s.client.Search(ctx, request)
	if err != nil {
		return LogResult{}, RequestFailure(ctx, LogTool, err)
	}
	if ctx.Err() != nil {
		return LogResult{}, RequestFailure(ctx, LogTool, ctx.Err())
	}
	if records == nil {
		records = []json.RawMessage{}
	}
	return LogResult{Request: request, Records: records}, nil
}
