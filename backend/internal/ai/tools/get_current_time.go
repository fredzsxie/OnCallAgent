package tools

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const GetCurrentTime = "get_current_time"
const timeDescription = "Get the current time in the configured IANA time zone, including its UTC offset and Unix timestamp in seconds. No input parameters are required."

type GetCurrentTimeInput struct{}

type GetCurrentTimeOutput struct {
	Timestamp   string `json:"timestamp"`
	TimeZone    string `json:"timezone"`
	UnixSeconds int64  `json:"unix_seconds"`
}

func NewGetCurrentTimeTool(location *time.Location, now func() time.Time) (tool.InvokableTool, error) {
	if location == nil {
		return nil, errors.New("time zone is required")
	}
	if now == nil {
		now = time.Now
	}
	return utils.InferTool(GetCurrentTime, timeDescription, func(ctx context.Context, input *GetCurrentTimeInput) (GetCurrentTimeOutput, error) {
		if err := ctx.Err(); err != nil {
			return GetCurrentTimeOutput{}, err
		}
		if input == nil {
			return GetCurrentTimeOutput{}, errors.New("time tool input must be a JSON object")
		}
		current := now().In(location)
		return GetCurrentTimeOutput{current.Format(time.RFC3339Nano), location.String(), current.Unix()}, nil
	})
}
