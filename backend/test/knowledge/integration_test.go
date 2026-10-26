package knowledge_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
)

func TestKnowledgeIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 to call real services and retain one test record")
	}
	values, err := godotenv.Read(filepath.Join("..", "..", ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cannot read backend/.env; check file syntax and permissions")
	}
	for name, value := range values {
		if _, exists := os.LookupEnv(name); !exists {
			t.Setenv(name, value)
		}
	}
	embeddingCfg, err := config.LoadEmbedding()
	if err != nil {
		t.Fatal(err)
	}
	milvusCfg, err := config.LoadMilvus()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	emb, err := client.NewEmbedding(ctx, embeddingCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer emb.Close()
	db, err := client.NewMilvus(ctx, milvusCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := db.GetVersion(ctx)
	if err != nil {
		t.Fatal("cannot read Milvus version")
	}
	exists, err := db.HasCollection(ctx, milvusCfg.Collection)
	if err != nil {
		t.Fatal("cannot check collection")
	}
	var existingID int64
	if exists {
		before, err := db.DescribeCollection(ctx, milvusCfg.Collection)
		if err != nil {
			t.Fatal("cannot describe existing collection")
		}
		existingID = before.ID
	}
	dimension, err := knowledge.Init(ctx, db, emb, milvusCfg)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		after, err := db.DescribeCollection(ctx, milvusCfg.Collection)
		if err != nil || after.ID != existingID {
			t.Fatal("initialization must preserve the existing collection ID")
		}
	}
	store, err := knowledge.NewStore(ctx, db, emb, milvusCfg)
	if err != nil {
		t.Fatal(err)
	}
	id, err := knowledge.NewID()
	if err != nil {
		t.Fatal(err)
	}
	id = "check_" + id
	doc := &schema.Document{ID: id, Content: "OnCallAgent 向量精简验收，记录编号：" + id, MetaData: map[string]any{"source": "knowledge_integration", "acceptance": true}}
	if _, err := store.Store(ctx, []*schema.Document{doc}); err != nil {
		t.Fatal(err)
	}
	t.Logf("retained record=%s; model=%s; dimension=%d; Milvus=%s", id, embeddingCfg.Model, dimension, version)
	found, err := store.Retrieve(ctx, doc.Content)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range found {
		if match.ID == id && match.Content == doc.Content && match.MetaData["source"] == "knowledge_integration" && match.Score() >= 0.99 {
			t.Logf("COSINE score=%.8f; existing collection preserved=%t", match.Score(), exists)
			return
		}
	}
	t.Fatal("the newly stored record was not retrieved with matching content, metadata and COSINE score")
}
