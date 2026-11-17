package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type observedTool struct {
	tool.InvokableTool
	name string
}

func (t observedTool) InvokableRun(ctx context.Context, input string, opts ...tool.Option) (output string, err error) {
	started := time.Now()
	// Preview actual tool I/O at its boundary, while keeping dependency errors private.
	logger.Info(ctx, "Tool", "开始执行工具", logger.F("tool", t.name), logger.F("req", toolPreview(input)))
	defer func() {
		fields := []logger.Field{logger.F("tool", t.name), logger.F("elapsed", time.Since(started)), logger.F("output_bytes", len(output))}
		if err != nil {
			errorType := logger.ErrorType(err)
			var failure *operations.Error
			if errors.As(err, &failure) {
				errorType = failure.Code
			}
			if errors.Is(err, retriever.ErrInvalidQuery) {
				errorType = "invalid_input"
			}
			logger.Warn(ctx, "Tool", "工具执行失败", append(fields, logger.F("error_type", errorType))...)
			return
		}
		if len(output) <= logger.PreviewLimit {
			data := []byte(output)
			var result map[string]json.RawMessage
			if json.Unmarshal(data, &result) == nil {
				for _, key := range []string{"alerts", "documents"} {
					if raw, ok := result[key]; ok {
						var items []json.RawMessage
						if json.Unmarshal(raw, &items) == nil {
							fields = append(fields, logger.F("count", len(items)))
						}
					}
				}
			}
			// Keep clock fields searchable without parsing the response preview.
			if t.name == GetCurrentTime {
				var clock GetCurrentTimeOutput
				if json.Unmarshal(data, &clock) == nil {
					fields = append(fields, logger.F("timestamp", clock.Timestamp), logger.F("timezone", clock.TimeZone))
				}
			}
		}
		logger.Info(ctx, "Tool", "工具执行完成", append(fields, logger.F("rsp", toolPreview(output)))...)
	}()
	return t.InvokableTool.InvokableRun(ctx, input, opts...)
}

func toolPreview(value string) string {
	if len(value) > logger.PreviewLimit {
		return "<truncated>"
	}
	return logger.JSONPreview([]byte(value))
}
