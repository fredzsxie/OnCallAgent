package tools

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

type operationTool[I, O any] struct {
	info *schema.ToolInfo
	run  func(context.Context, *I) (O, error)
}

func newOperationTool[I, O any](name, description string, run func(context.Context, *I) (O, error)) (tool.InvokableTool, error) {
	params, err := utils.GoStruct2ParamsOneOf[I]()
	if err != nil {
		return nil, err
	}
	return &operationTool[I, O]{info: &schema.ToolInfo{Name: name, Desc: description, ParamsOneOf: params}, run: run}, nil
}

func (t *operationTool[I, O]) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *operationTool[I, O]) InvokableRun(ctx context.Context, input string, _ ...tool.Option) (string, error) {
	if ctx.Err() != nil {
		return "", operations.RequestFailure(ctx, t.info.Name, ctx.Err())
	}
	var value *I
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return "", operations.Failure(t.info.Name, operations.ErrInvalidInput)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", operations.Failure(t.info.Name, operations.ErrInvalidInput)
	}
	output, err := t.run(ctx, value)
	if err != nil {
		return "", operations.RequestFailure(ctx, t.info.Name, err)
	}
	data, err := json.Marshal(output)
	if err != nil {
		return "", operations.Failure(t.info.Name, operations.ErrInvalidResponse)
	}
	return string(data), nil
}
