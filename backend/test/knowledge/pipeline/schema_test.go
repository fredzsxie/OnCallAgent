package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/compose"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
)

type compiledGraph struct{ info *compose.GraphInfo }

func (c *compiledGraph) OnFinish(_ context.Context, info *compose.GraphInfo) { c.info = info }

func TestSchemaMatchesCompiledGraph(t *testing.T) {
	data, err := os.ReadFile("../../../internal/ai/agent/knowledge_index_pipeline/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Name       string                 `json:"name"`
		InputType  struct{ Title string } `json:"input_type"`
		OutputType struct{ Title string } `json:"output_type"`
		Nodes      []struct {
			Key, Type       string
			ComponentSchema struct{ Identifier, Component, Name string } `json:"component_schema"`
		}
		Edges []struct {
			Source string `json:"source_node_key"`
			Target string `json:"target_node_key"`
		}
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	callback := &compiledGraph{}
	compose.InitGraphCompileCallbacks([]compose.GraphCompileCallback{callback})
	defer compose.InitGraphCompileCallbacks(nil)
	build(t, settings(t), &testutil.MemoryStore{})
	info := callback.info
	if info == nil || info.Name != schema.Name || info.Name != "KnowledgeIndexing" {
		t.Fatal("graph name differs")
	}
	if info.InputType != reflect.TypeOf(document.Source{}) || schema.InputType.Title != "document.Source" || info.OutputType != reflect.TypeOf([]string{}) || schema.OutputType.Title != "[]string" {
		t.Fatal("graph contract differs")
	}
	if len(schema.Nodes) != len(info.Nodes)+2 || len(schema.Nodes) != 5 {
		t.Fatal("node count differs")
	}
	for _, node := range schema.Nodes {
		if node.Type == "start" || node.Type == "end" {
			continue
		}
		actual, ok := info.Nodes[node.Key]
		if !ok || string(actual.Component) != node.Type || node.ComponentSchema.Component != node.Type {
			t.Fatalf("node differs: %s", node.Key)
		}
		if node.ComponentSchema.Identifier != "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline/"+node.Key {
			t.Fatalf("wrong component identifier: %s", node.Key)
		}
		component, ok := actual.Instance.(interface{ GetType() string })
		if !ok || component.GetType() != node.ComponentSchema.Name {
			t.Fatal("component does not describe actual adapter")
		}
	}
	edges := map[string][]string{}
	boundary := func(key string) string {
		if key == "start" {
			return compose.START
		}
		if key == "end" {
			return compose.END
		}
		return key
	}
	for _, edge := range schema.Edges {
		source := boundary(edge.Source)
		edges[source] = append(edges[source], boundary(edge.Target))
	}
	if len(schema.Edges) != 4 || !reflect.DeepEqual(edges, info.Edges) {
		t.Fatalf("edges differ: schema=%v runtime=%v", edges, info.Edges)
	}
}
