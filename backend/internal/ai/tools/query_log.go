package tools

import (
	"github.com/cloudwego/eino/components/tool"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

const QueryLog = operations.LogTool
const logDescription = "Search Tencent Cloud CLS logs using an explicit CQL query. Region and a single topic may come from configuration or input. Times use RFC3339 with a time zone; omit both for the configured recent interval. Empty records means no matching data."

func NewQueryLogTool(service operations.LogQuerier) (tool.InvokableTool, error) {
	if service == nil {
		return nil, operations.Failure(QueryLog, operations.ErrConfiguration)
	}
	return newOperationTool(QueryLog, logDescription, service.Query)
}
