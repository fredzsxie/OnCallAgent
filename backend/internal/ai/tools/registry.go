package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

var (
	ErrUnknownTool  = errors.New("unknown tool")
	ErrDisabledTool = errors.New("tool is disabled")
)

type Registry struct{ tools map[string]tool.InvokableTool }

type Dependencies struct {
	Retriever  *retriever.MilvusRetriever
	Now        func() time.Time
	Prometheus operations.AlertQuerier
}

type Definition struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

func Enabled(cfg config.Tools, name string) (bool, error) {
	switch name {
	case GetCurrentTime:
		return cfg.GetCurrentTimeEnabled, nil
	case QueryInternalDocs:
		return cfg.QueryInternalDocsEnabled, nil
	case QueryPrometheusAlerts:
		return cfg.QueryPrometheusAlertsEnabled, nil
	default:
		return false, ErrUnknownTool
	}
}

func NewRegistry(cfg config.Tools, deps Dependencies) (*Registry, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	registry := &Registry{tools: make(map[string]tool.InvokableTool)}
	if cfg.GetCurrentTimeEnabled {
		location, err := time.LoadLocation(cfg.TimeZone)
		if err != nil {
			return nil, err
		}
		t, err := NewGetCurrentTimeTool(location, deps.Now)
		if err != nil {
			return nil, err
		}
		registry.tools[GetCurrentTime] = t
	}
	if cfg.QueryInternalDocsEnabled {
		t, err := NewQueryInternalDocsTool(deps.Retriever)
		if err != nil {
			return nil, err
		}
		registry.tools[QueryInternalDocs] = t
	}
	if cfg.QueryPrometheusAlertsEnabled {
		t, err := NewPrometheusAlertsQueryTool(deps.Prometheus)
		if err != nil {
			return nil, err
		}
		registry.tools[QueryPrometheusAlerts] = t
	}
	for name, t := range registry.tools {
		registry.tools[name] = observedTool{t, name}
	}
	return registry, nil
}

func (r *Registry) Tools() []tool.BaseTool {
	result := make([]tool.BaseTool, 0, len(r.tools))
	for _, name := range []string{GetCurrentTime, QueryInternalDocs, QueryPrometheusAlerts} {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}

func (r *Registry) Invoke(ctx context.Context, name, input string) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		switch name {
		case GetCurrentTime, QueryInternalDocs, QueryPrometheusAlerts:
			return "", ErrDisabledTool
		default:
			return "", ErrUnknownTool
		}
	}
	return t.InvokableRun(ctx, input)
}

// Definitions infer the same Go types as the runnable tools without opening external clients.
func Definitions(cfg config.Tools) ([]Definition, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	result := make([]Definition, 0, 3)
	if cfg.GetCurrentTimeEnabled {
		d, err := definition[*GetCurrentTimeInput, GetCurrentTimeOutput](GetCurrentTime, timeDescription)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if cfg.QueryInternalDocsEnabled {
		d, err := definition[*QueryInternalDocsInput, retriever.Result](QueryInternalDocs, queryDescription)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if cfg.QueryPrometheusAlertsEnabled {
		d, err := definition[*PrometheusAlertsInput, operations.AlertsResult](QueryPrometheusAlerts, alertsDescription)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}

func Select(cfg config.Tools, name string) (config.Tools, error) {
	enabled, err := Enabled(cfg, name)
	if err != nil {
		return cfg, err
	}
	if !enabled {
		return cfg, ErrDisabledTool
	}
	cfg.GetCurrentTimeEnabled = name == GetCurrentTime
	cfg.QueryInternalDocsEnabled = name == QueryInternalDocs
	cfg.QueryPrometheusAlertsEnabled = name == QueryPrometheusAlerts
	return cfg, nil
}

func definition[I, O any](name, description string) (Definition, error) {
	input, err := utils.GoStruct2ParamsOneOf[I]()
	if err != nil {
		return Definition{}, err
	}
	output, err := utils.GoStruct2ParamsOneOf[O]()
	if err != nil {
		return Definition{}, err
	}
	inputSchema, err := input.ToJSONSchema()
	if err != nil {
		return Definition{}, err
	}
	outputSchema, err := output.ToJSONSchema()
	if err != nil {
		return Definition{}, err
	}
	inputJSON, err := json.Marshal(inputSchema)
	if err != nil {
		return Definition{}, err
	}
	outputJSON, err := json.Marshal(outputSchema)
	if err != nil {
		return Definition{}, err
	}
	return Definition{name, description, inputJSON, outputJSON}, nil
}
