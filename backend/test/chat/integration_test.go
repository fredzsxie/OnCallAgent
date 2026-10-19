package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/chat_pipeline"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/tools"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/mem"
	"github.com/joho/godotenv"
)

func TestChatMemoryIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 for real HTTP chat/SSE memory with read-only stage06 retrieval")
	}
	values, err := godotenv.Read("../../.env")
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
	collection := os.Getenv("ONCALL_CHAT_INTEGRATION_COLLECTION")
	if collection == "" {
		collection = "stage06_acceptance_bb0770e598ee2fff70d0f800ee1621ef"
	}
	if !strings.HasPrefix(collection, "stage06_acceptance_") {
		t.Fatal("integration requires a stage06 acceptance collection")
	}
	dbCfg.Collection = collection
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	model, err := client.NewOpenAI(ctx, modelCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
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
	store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	recall, err := retriever.NewMilvusRetriever(store, config.Retrieval{TopK: 3, ScoreThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistry(config.Tools{QueryInternalDocsEnabled: true, GetCurrentTimeEnabled: true, TimeZone: "Asia/Shanghai"}, tools.Dependencies{Retriever: recall})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := pipeline.BuildChatAgent(ctx, pipeline.Config{Agent: config.ChatAgent{MaxSteps: 12, Timeout: 60 * time.Second}, TimeZone: "Asia/Shanghai"}, pipeline.Dependencies{Model: model.ToolCallingModel(), Retriever: recall, Tools: registry.Tools()})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := mem.NewStore(config.ChatMemory{MaxRounds: 3, TTL: 30 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := chatservice.NewChatService(graph, memory, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	api := startAPI(t, service)
	unique, err := knowledge.NewID()
	if err != nil {
		t.Fatal(err)
	}
	id := "stage07-" + unique
	code := "Aurora-" + unique[:10]
	t.Logf("read-only collection=%s chat_model=%s session=%s", collection, modelCfg.Model, id)
	r := post(t, api+"/api/chat", id, "请记住我的值班代号是 "+code+"，这只是本会话的代号。只回复已记住，不需要调用工具。")
	first, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("remember failed: %d %s", r.StatusCode, first)
	}
	events := readSSE(t, post(t, api+"/api/chat_stream", id, "我的值班代号是什么？只回复代号，不需要调用工具。"))
	var answer strings.Builder
	for _, event := range events {
		if event.name == "error" {
			t.Fatalf("stream failed: %v", event.data)
		}
		if event.name == "message" {
			answer.WriteString(event.data["content"].(string))
		}
	}
	if events[len(events)-1].name != "done" || !strings.Contains(answer.String(), code) {
		t.Fatalf("SSE did not recall ordinary history: %q", answer.String())
	}
	other := post(t, api+"/api/chat", id+"-isolated", "我的值班代号是什么？不知道就回复不知道，不需要调用工具。")
	var result struct {
		Message string
		Data    struct{ Answer string }
	}
	err = json.NewDecoder(other.Body).Decode(&result)
	other.Body.Close()
	if err != nil || other.StatusCode != 200 || strings.Contains(result.Data.Answer, code) {
		t.Fatal("session isolation failed")
	}
	h := snapshot(t, memory, id)
	if len(h) != 4 || h[1].Role != "assistant" || h[3].Role != "assistant" {
		t.Fatal("wrong saved turn roles")
	}
	t.Logf("recall=%q isolated=%q saved_messages=%d", answer.String(), result.Data.Answer, len(h))
}
