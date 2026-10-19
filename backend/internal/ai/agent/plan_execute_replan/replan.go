package plan_execute_replan

import (
	"context"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

func NewReplanner(ctx context.Context, m model.ToolCallingChatModel, available string) (adk.Agent, error) {
	params, err := utils.GoStruct2ParamsOneOf[reportSections]()
	if err != nil {
		return nil, err
	}
	return planexecute.NewReplanner(ctx, &planexecute.ReplannerConfig{ChatModel: &planningModel{base: m},
		RespondTool: &schema.ToolInfo{Name: "Respond", Desc: "Finish with all five report sections. Explicitly state missing evidence and disabled or failed tools.", ParamsOneOf: params},
		NewPlan:     func(context.Context) planexecute.Plan { return &plan{} },
		GenInputFn: func(ctx context.Context, in *planexecute.ExecutionContext) ([]adk.Message, error) {
			p, err := in.Plan.MarshalJSON()
			if err != nil {
				return nil, err
			}
			msgs, err := planexecute.ReplannerPrompt.Format(ctx, map[string]any{"input": in.UserInput, "plan": string(p), "executed_steps": in.ExecutedSteps, "plan_tool": "Plan", "respond_tool": "Respond"})
			if err != nil {
				return nil, err
			}
			msgs[0].Content += "\n" + instructions + "\nEnabled tools: " + available + `
Finish with Respond as soon as the available evidence supports a report with explicit limitations. Do not create a step merely to write the report. If no alerts were found, one relevant documentation search is sufficient; do not keep searching for nonexistent alert instances or retry unavailable tools.`
			return msgs, nil
		},
	})
}
