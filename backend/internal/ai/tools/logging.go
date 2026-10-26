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
	logger.Debug(ctx, "Tool", "开始执行工具", logger.F("tool", t.name))
	defer func() {
		fields := []logger.Field{logger.F("tool", t.name), logger.F("elapsed", time.Since(started))}
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
		var result map[string]json.RawMessage
		if json.Unmarshal([]byte(output), &result) == nil {
			for _, key := range []string{"alerts", "documents"} {
				if raw, ok := result[key]; ok {
					var items []json.RawMessage
					if json.Unmarshal(raw, &items) == nil {
						fields = append(fields, logger.F("count", len(items)))
					}
				}
			}
		}
		logger.Info(ctx, "Tool", "工具执行完成", fields...)
	}()
	return t.InvokableTool.InvokableRun(ctx, input, opts...)
}
