package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type backend struct {
	calls int
	docs  []*schema.Document
	err   error
}

func (b *backend) Retrieve(context.Context, string, ...eino.Option) ([]*schema.Document, error) {
	b.calls++
	return b.docs, b.err
}

func settings() config.Tools {
	return config.Tools{QueryInternalDocsEnabled: true, GetCurrentTimeEnabled: true, TimeZone: "Asia/Shanghai"}
}
func makeRetriever(t *testing.T, b *backend) *retriever.MilvusRetriever {
	t.Helper()
	r, err := retriever.NewMilvusRetriever(b, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestQueryToolJSONAndDependencyReuse(t *testing.T) {
	b := &backend{docs: []*schema.Document{(&schema.Document{ID: "one", Content: "Restart the service", MetaData: map[string]any{"source": "guide.md", "title": "Recovery"}}).WithScore(0.8)}}
	registry, err := tools.NewRegistry(settings(), makeRetriever(t, b), nil)
	if err != nil || b.calls != 0 {
		t.Fatal("registry called retriever during construction")
	}
	for range 2 {
		output, err := registry.Invoke(context.Background(), tools.QueryInternalDocs, `{"query":" restart "}`)
		var result retriever.Result
		if err != nil || json.Unmarshal([]byte(output), &result) != nil || result.Query != "restart" || len(result.Documents) != 1 || result.Documents[0].Score != 0.8 {
			t.Fatalf("tool output is not a JSON object: %s %v", output, err)
		}
	}
	if b.calls != 2 {
		t.Fatal("tool did not reuse retriever")
	}
	for _, input := range []string{`null`, `{}`, `{"query":"  "}`, `{`, `[]`, `{"query":123}`} {
		if _, err := registry.Invoke(context.Background(), tools.QueryInternalDocs, input); err == nil {
			t.Fatalf("invalid input accepted: %s", input)
		}
	}
	if b.calls != 2 {
		t.Fatal("invalid input reached backend")
	}
	b.docs = nil
	output, err := registry.Invoke(context.Background(), tools.QueryInternalDocs, `{"query":"unrelated"}`)
	if err != nil || !strings.Contains(output, `"documents":[]`) {
		t.Fatalf("empty result: %s %v", output, err)
	}
	b.err = errors.New("fixture-secret: backend unavailable")
	output, err = registry.Invoke(context.Background(), tools.QueryInternalDocs, `{"query":"restart"}`)
	if !errors.Is(err, retriever.ErrQueryFailed) || output != "" || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("unsafe tool failure: %s %v", output, err)
	}
	b.err = context.DeadlineExceeded
	if _, err := registry.Invoke(context.Background(), tools.QueryInternalDocs, `{"query":"restart"}`); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout lost")
	}
}

func TestTimeToolUsesConfiguredZoneAndOneClockRead(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	fixed := time.Date(2026, 10, 16, 0, 0, 0, 123456789, time.UTC)
	calls := 0
	timeTool, err := tools.NewGetCurrentTimeTool(location, func() time.Time { calls++; return fixed })
	if err != nil {
		t.Fatal(err)
	}
	output, err := timeTool.InvokableRun(context.Background(), `{}`)
	var value tools.GetCurrentTimeOutput
	if err != nil || json.Unmarshal([]byte(output), &value) != nil || calls != 1 || value.Timestamp != "2026-10-16T08:00:00.123456789+08:00" || value.TimeZone != "Asia/Shanghai" || value.UnixSeconds != fixed.Unix() {
		t.Fatalf("time output: %s %v clock reads=%d", output, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := timeTool.InvokableRun(ctx, `{}`); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancelled tool read the clock")
	}
	for _, input := range []string{`null`, `{`, `[]`} {
		if _, err := timeTool.InvokableRun(context.Background(), input); err == nil {
			t.Fatal("invalid time input accepted")
		}
	}
	if _, err := tools.NewGetCurrentTimeTool(nil, nil); err == nil {
		t.Fatal("missing location accepted")
	}
}

func TestRegistrySwitchesAndSchemas(t *testing.T) {
	for _, docsEnabled := range []bool{false, true} {
		for _, timeEnabled := range []bool{false, true} {
			cfg := settings()
			cfg.QueryInternalDocsEnabled, cfg.GetCurrentTimeEnabled = docsEnabled, timeEnabled
			var r *retriever.MilvusRetriever
			if docsEnabled {
				r = makeRetriever(t, &backend{})
			}
			registry, err := tools.NewRegistry(cfg, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			definitions, err := tools.Definitions(cfg)
			if err != nil || len(registry.Tools()) != len(definitions) {
				t.Fatal("definitions and registered tools differ")
			}
			for i, tool := range registry.Tools() {
				info, err := tool.Info(context.Background())
				if err != nil || info.Name != definitions[i].Name {
					t.Fatal("tool names differ")
				}
				actualSchema, err := info.ParamsOneOf.ToJSONSchema()
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(actualSchema)
				var a, b any
				if json.Unmarshal(data, &a) != nil || json.Unmarshal(definitions[i].InputSchema, &b) != nil || !reflect.DeepEqual(a, b) {
					t.Fatal("listed schema differs from runnable tool")
				}
				var output map[string]any
				if json.Unmarshal(definitions[i].OutputSchema, &output) != nil || output["type"] != "object" {
					t.Fatal("missing output schema")
				}
				if info.Name == tools.QueryInternalDocs {
					var input struct{ Required []string }
					if json.Unmarshal(data, &input) != nil || !reflect.DeepEqual(input.Required, []string{"query"}) {
						t.Fatal("query is not required in schema")
					}
				}
			}
			if !docsEnabled {
				if _, err := registry.Invoke(context.Background(), tools.QueryInternalDocs, `{}`); !errors.Is(err, tools.ErrDisabledTool) {
					t.Fatal("disabled docs tool accessible")
				}
			}
			if !timeEnabled {
				if _, err := registry.Invoke(context.Background(), tools.GetCurrentTime, `{}`); !errors.Is(err, tools.ErrDisabledTool) {
					t.Fatal("disabled time tool accessible")
				}
			}
			if _, err := registry.Invoke(context.Background(), "shell", `{}`); !errors.Is(err, tools.ErrUnknownTool) {
				t.Fatal("unknown tool accepted")
			}
		}
	}
	if _, err := tools.NewRegistry(settings(), nil, nil); err == nil {
		t.Fatal("enabled docs tool accepted missing dependency")
	}
}
