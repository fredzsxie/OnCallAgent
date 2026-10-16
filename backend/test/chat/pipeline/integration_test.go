package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/tool"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	indexing "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	recall "github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
)

type toolTrace struct {
	sync.Mutex
	names []string
}
type observedTool struct {
	tool.InvokableTool
	trace *toolTrace
	name  string
}

func (t observedTool) InvokableRun(ctx context.Context, input string, opts ...tool.Option) (string, error) {
	t.trace.Lock()
	t.trace.names = append(t.trace.names, t.name)
	t.trace.Unlock()
	return t.InvokableTool.InvokableRun(ctx, input, opts...)
}
func (t *toolTrace) reset() { t.Lock(); defer t.Unlock(); t.names = nil }
func (t *toolTrace) snapshot() []string {
	t.Lock()
	defer t.Unlock()
	return append([]string(nil), t.names...)
}

func TestChatAgentIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 for a retained isolated stage06 collection and real tool-calling model")
	}
	values, err := godotenv.Read("../../../.env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cannot read backend/.env")
	}
	for key, value := range values {
		if _, ok := os.LookupEnv(key); !ok {
			t.Setenv(key, value)
		}
	}
	modelCfg, err := config.LoadChatModel()
	if err != nil {
		t.Fatal(err)
	}
	embCfg, err := config.LoadEmbedding()
	if err != nil {
		t.Fatal(err)
	}
	dbCfg, err := config.LoadMilvus()
	if err != nil {
		t.Fatal(err)
	}
	unique, err := knowledge.NewID()
	if err != nil {
		t.Fatal(err)
	}
	dbCfg.Collection = "stage06_acceptance_" + unique
	ctx, cancel := context.WithTimeout(context.Background(), 230*time.Second)
	defer cancel()
	modelClient, err := client.NewOpenAI(ctx, modelCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer modelClient.Close()
	emb, err := client.NewEmbedding(ctx, embCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer emb.Close()
	db, err := client.NewMilvus(ctx, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dim, err := knowledge.Init(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained collection=%s database=%s chat_model=%s dimension=%d", dbCfg.Collection, dbCfg.Database, modelCfg.Model, dim)
	store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../retriever/testdata")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := indexing.BuildKnowledgeIndexing(ctx, indexing.Config{Root: root, Knowledge: config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 4096, ChunkSize: 1000, ChunkOverlap: 100, BatchSize: 2}}, store)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := idx.Invoke(ctx, document.Source{URI: "auth_runbook.md"})
	if err != nil || len(ids) != 3 {
		t.Fatalf("seed: %v %v", ids, err)
	}
	t.Logf("source=auth_runbook.md ids=%v", ids)
	r, err := recall.NewMilvusRetriever(store, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistry(config.Tools{QueryInternalDocsEnabled: true, GetCurrentTimeEnabled: true, TimeZone: "Asia/Shanghai"}, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	trace := &toolTrace{}
	var registered []tool.BaseTool
	for _, base := range registry.Tools() {
		info, err := base.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		registered = append(registered, observedTool{InvokableTool: base.(tool.InvokableTool), trace: trace, name: info.Name})
	}
	g, err := pipeline.BuildChatAgent(ctx, pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: 60 * time.Second}, TimeZone: "Asia/Shanghai"}, pipeline.Dependencies{Model: modelClient.ToolCallingModel(), Retriever: r, Tools: registered})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, requiredTool string
		stream                    bool
	}{
		{"documentation", "AuroraAuth 密钥轮转后出现401，如何恢复并验证？请根据资料回答并注明来源文件。", "", false},
		{"time", "现在上海准确时间是几点？请实际调用 get_current_time 工具，并返回工具给出的完整时间戳。", tools.GetCurrentTime, false},
		{"supplemental", "请调用 query_internal_docs 查询 AuroraAuth 验证恢复的步骤，只用一句话回答并注明文件名。", tools.QueryInternalDocs, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace.reset()
			var answer string
			if tc.stream {
				sr, err := g.Stream(ctx, &pipeline.UserMessage{ID: tc.name, Query: tc.query})
				if err != nil {
					t.Fatalf("stream start: %v; observed tools=%v", err, trace.snapshot())
				}
				defer sr.Close()
				for {
					chunk, err := sr.Recv()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("stream failed: %v; observed tools=%v", err, trace.snapshot())
					}
					if len(chunk.ToolCalls) > 0 {
						t.Fatalf("internal tool call leaked after content=%q", answer)
					}
					answer += chunk.Content
				}
			} else {
				msg, err := g.Invoke(ctx, &pipeline.UserMessage{ID: tc.name, Query: tc.query})
				if err != nil {
					t.Fatal(err)
				}
				answer = msg.Content
			}
			if strings.TrimSpace(answer) == "" {
				t.Fatal("empty real answer")
			}
			called := trace.snapshot()
			if tc.requiredTool != "" {
				found := false
				for _, name := range called {
					found = found || name == tc.requiredTool
				}
				if !found {
					t.Fatalf("configured model did not call %s; observed=%v", tc.requiredTool, called)
				}
			}
			if tc.name != "time" && !strings.Contains(answer, "auth_runbook.md") {
				t.Error("answer did not cite the supplied source")
			}
			if tc.name == "documentation" && !strings.Contains(answer, "signing-key-cache") {
				t.Error("answer did not include recovery evidence")
			}
			safe, _ := json.Marshal(answer)
			t.Logf("tools=%v answer=%s", called, safe)
		})
	}
}
