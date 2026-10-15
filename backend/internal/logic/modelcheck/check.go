package modelcheck

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
)

type Result struct {
	ConfiguredModel         string   `json:"configured_model"`
	AvailableModels         []string `json:"available_models"`
	ModelIDVerified         bool     `json:"model_id_verified"`
	ChatCompletionAvailable bool     `json:"chat_completion_available"`
}

func Check(ctx context.Context, model *client.OpenAI) (*Result, error) {
	ids, err := model.Models(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(ids, model.ModelID()) {
		return nil, fmt.Errorf("configured model ID %q not found in GET /models; available IDs: %s", model.ModelID(), strings.Join(ids, ", "))
	}
	if _, err := model.Generate(ctx, "Please reply with OK."); err != nil {
		return nil, fmt.Errorf("chat completion check failed: %w", err)
	}
	return &Result{
		ConfiguredModel: model.ModelID(), AvailableModels: ids,
		ModelIDVerified: true, ChatCompletionAvailable: true,
	}, nil
}
