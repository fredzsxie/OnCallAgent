package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
)

type graphCapture struct{ info *compose.GraphInfo }

func (c *graphCapture) OnFinish(_ context.Context, info *compose.GraphInfo) {
	if info.Name == "ChatAgent" {
		c.info = info
	}
}

func TestSchemaMatchesCompiledGraph(t *testing.T) {
	data, err := os.ReadFile("../../../internal/ai/agent/chat_pipeline/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Name   string
		Mode   string                 `json:"node_trigger_mode"`
		Input  struct{ Title string } `json:"input_type"`
		Output struct{ Title string } `json:"output_type"`
		Nodes  []struct {
			Key, Type string
			Options   struct {
				OutputKey string `json:"output_key"`
			} `json:"node_option"`
			Component struct {
				Identifier, Component string
				Input                 struct{ Title string } `json:"input_type"`
				Output                struct{ Title string } `json:"output_type"`
			} `json:"component_schema"`
		}
		Edges []struct {
			Source string `json:"source_node_key"`
			Target string `json:"target_node_key"`
		}
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	capture := &graphCapture{}
	compose.InitGraphCompileCallbacks([]compose.GraphCompileCallback{capture})
	defer compose.InitGraphCompileCallbacks(nil)
	build(t, settings(), pipeline.Dependencies{Model: &scriptedModel{generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("ok", nil), nil
	}}, Retriever: retrieverFunc(documents)})
	info := capture.info
	if info == nil || spec.Name != info.Name || spec.Mode != "AllPredecessor" || len(spec.Nodes) != 7 || len(info.Nodes) != 5 {
		t.Fatal("graph identity differs")
	}
	if info.InputType != reflect.TypeOf((*pipeline.UserMessage)(nil)) || info.OutputType != reflect.TypeOf((*schema.Message)(nil)) || spec.Input.Title != "*UserMessage" || spec.Output.Title != "*schema.Message" {
		t.Fatal("graph types differ")
	}
	for _, n := range spec.Nodes {
		if n.Type == "start" || n.Type == "end" {
			continue
		}
		actual, ok := info.Nodes[n.Key]
		if !ok || string(actual.Component) != n.Type || n.Component.Component != n.Type || actual.Name != n.Key || actual.OutputKey != n.Options.OutputKey {
			t.Fatalf("node differs: %s %+v", n.Key, actual)
		}
		if n.Component.Identifier != "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline/"+n.Key {
			t.Fatal("wrong component identifier")
		}
		if n.Type == "Lambda" {
			input := actual.InputType.String()
			if n.Key != "ReActAgent" {
				input = "*UserMessage"
			}
			if input != n.Component.Input.Title || strings.ReplaceAll(actual.OutputType.String(), "interface {}", "any") != n.Component.Output.Title {
				t.Fatalf("lambda contract differs: %s", n.Key)
			}
		}
	}
	edges := map[string][]string{}
	for _, e := range spec.Edges {
		edges[e.Source] = append(edges[e.Source], e.Target)
	}
	if len(spec.Edges) != 7 || !reflect.DeepEqual(edges, info.Edges) {
		t.Fatalf("edges differ: schema=%v actual=%v", edges, info.Edges)
	}
	if info.Nodes["MilvusRetriever"].OutputKey != "documents" {
		t.Fatal("documents mapping missing")
	}
}
