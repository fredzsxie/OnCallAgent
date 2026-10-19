package retriever_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/document"
	knowledgepipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/knowledge_index_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
)

func TestRetrievalIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 to create and retain an isolated stage05 acceptance collection")
	}
	values, err := godotenv.Read("../../.env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cannot read backend/.env")
	}
	for name, value := range values {
		if _, ok := os.LookupEnv(name); !ok {
			t.Setenv(name, value)
		}
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
	dbCfg.Collection = "stage05_acceptance_" + unique
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
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
	dimension, err := knowledge.Init(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained database=%s collection=%s model=%s dimension=%d", dbCfg.Database, dbCfg.Collection, embCfg.Model, dimension)
	store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := knowledgepipeline.BuildKnowledgeIndexing(ctx, knowledgepipeline.Config{Root: root, Knowledge: config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 4096, ChunkSize: 1000, ChunkOverlap: 100, BatchSize: 2}}, store)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := graph.Invoke(ctx, document.Source{URI: "auth_runbook.md"})
	if err != nil || len(ids) != 3 {
		t.Fatalf("expected three heading chunks: %v %v", ids, err)
	}
	r, err := retriever.NewMilvusRetriever(store, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		name, query string
		min         int
	}{
		{"related", "AuroraAuth 密钥轮转后出现401，如何刷新签名密钥缓存恢复认证？", 1},
		{"unrelated", "介绍如何用黄油制作法式可颂的烘焙温度和面团折叠方法。", 0},
		{"cross_chunk", "AuroraAuth 密钥轮转后令牌签名校验失败，应该怎么恢复，并如何确认认证已经恢复？", 2},
	}
	for _, tc := range queries {
		result, err := r.Search(ctx, tc.query)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Documents) < tc.min || (tc.min == 0 && len(result.Documents) != 0) {
			t.Errorf("%s: unexpected hit count %d", tc.name, len(result.Documents))
		}
		titles := map[string]bool{}
		for _, hit := range result.Documents {
			if hit.Source != "auth_runbook.md" || hit.Title == "" || hit.Score < 0.5 || hit.Content == "" {
				t.Fatal("incomplete/low-score hit")
			}
			titles[hit.Title] = true
			t.Logf("%s id=%s source=%s title=%s score=%.6f", tc.name, hit.ID, hit.Source, hit.Title, hit.Score)
		}
		if tc.name == "cross_chunk" && (!titles["恢复认证"] || !titles["验证恢复"]) {
			t.Error("cross-chunk question did not recall both recovery and verification")
		}
		if tc.name == "unrelated" {
			raw, err := r.Retrieve(ctx, tc.query)
			if err != nil || len(raw) != 0 {
				t.Fatal("empty Eino result differs")
			}
			unfiltered, err := retriever.NewMilvusRetriever(store, config.Retrieval{TopK: 3, ScoreThreshold: -1})
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := unfiltered.Search(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			for _, hit := range candidates.Documents {
				t.Logf("unrelated raw id=%s score=%.6f threshold=0.5", hit.ID, hit.Score)
			}
		}
		registry, err := tools.NewRegistry(config.Tools{QueryInternalDocsEnabled: true, TimeZone: "Asia/Shanghai"}, tools.Dependencies{Retriever: r})
		if err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(tools.QueryInternalDocsInput{Query: tc.query})
		output, err := registry.Invoke(ctx, tools.QueryInternalDocs, string(input))
		if err != nil {
			t.Fatal(err)
		}
		var toolResult retriever.Result
		if json.Unmarshal([]byte(output), &toolResult) != nil {
			t.Fatal("tool returned non-object JSON")
		}
		keySet := func(value retriever.Result) []string {
			keys := []string{}
			for _, d := range value.Documents {
				keys = append(keys, d.ID)
			}
			sort.Strings(keys)
			return keys
		}
		if !reflect.DeepEqual(keySet(result), keySet(toolResult)) {
			t.Fatal("tool and retriever recall different IDs")
		}
	}
	if !strings.HasPrefix(dbCfg.Collection, "stage05_acceptance_") {
		t.Fatal("default collection was not isolated")
	}
}
