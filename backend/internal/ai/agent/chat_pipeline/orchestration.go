package chat_pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

// Generated with Eino Dev 1.3.0 nclc; adapted for dependency injection and bounded execution.
func BuildChatAgent(ctx context.Context, cfg Config, deps Dependencies) (compose.Runnable[*UserMessage, *schema.Message], error) {
	if err := cfg.Agent.Validate(); err != nil {
		return nil, err
	}
	if err := (config.Tools{TimeZone: cfg.TimeZone}).Validate(); err != nil {
		return nil, err
	}
	if deps.Retriever == nil {
		return nil, errors.New("retriever is required")
	}
	location, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return nil, err
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	g := compose.NewGraph[*UserMessage, *schema.Message](compose.WithGenLocalState(func(context.Context) *runState {
		return &runState{Deadline: time.Now().Add(cfg.Agent.Timeout)}
	}))
	if err = g.AddLambdaNode("InputToRag", compose.InvokableLambda(newInputToRagLambda), compose.WithNodeName("InputToRag")); err != nil {
		return nil, err
	}
	if err = g.AddLambdaNode("InputToChat", compose.InvokableLambda(newInputToChatLambda(location, deps.Now)), compose.WithNodeName("InputToChat")); err != nil {
		return nil, err
	}
	if err = g.AddRetrieverNode("MilvusRetriever", readableRetriever{base: deps.Retriever}, compose.WithOutputKey("documents"), compose.WithNodeName("MilvusRetriever")); err != nil {
		return nil, err
	}
	if err = g.AddChatTemplateNode("ChatTemplate", newChatTemplate(), compose.WithNodeName("ChatTemplate")); err != nil {
		return nil, err
	}
	flow, err := newReactAgentLambda(ctx, cfg, deps)
	if err != nil {
		return nil, err
	}
	if err = g.AddLambdaNode("ReActAgent", flow, compose.WithNodeName("ReActAgent")); err != nil {
		return nil, err
	}
	for _, edge := range [][2]string{
		{compose.START, "InputToRag"}, {compose.START, "InputToChat"},
		{"InputToRag", "MilvusRetriever"}, {"MilvusRetriever", "ChatTemplate"},
		{"InputToChat", "ChatTemplate"}, {"ChatTemplate", "ReActAgent"}, {"ReActAgent", compose.END},
	} {
		if err = g.AddEdge(edge[0], edge[1]); err != nil {
			return nil, err
		}
	}
	return g.Compile(ctx, compose.WithGraphName("ChatAgent"), compose.WithNodeTriggerMode(compose.AllPredecessor))
}
