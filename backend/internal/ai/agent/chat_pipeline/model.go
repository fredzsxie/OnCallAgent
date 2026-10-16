package chat_pipeline

import (
	"errors"
	"github.com/cloudwego/eino/components/model"
)

func newChatModel(deps Dependencies) (model.ToolCallingChatModel, error) {
	if deps.Model == nil {
		return nil, errors.New("tool-calling chat model is required")
	}
	return deps.Model, nil
}
