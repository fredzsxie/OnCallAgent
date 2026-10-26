package pipeline_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
	milvus "github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

func TestPipelineIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 to write real embeddings and retain stage04_1 acceptance chunks")
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
	embCfg, err := config.LoadEmbedding()
	if err != nil {
		t.Fatal(err)
	}
	dbCfg, err := config.LoadMilvus()
	if err != nil {
		t.Fatal(err)
	}
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
	store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings(t)
	cfg.Knowledge.ChunkSize, cfg.Knowledge.ChunkOverlap = 128, 16
	unique, err := knowledge.NewID()
	if err != nil {
		t.Fatal(err)
	}
	source := "stage04_1_acceptance_" + unique + ".md"
	write(t, cfg, source, "# Pipeline acceptance\nCheck the dependency health endpoint before restarting the service.\n\n## Recovery\nRecord the incident and verify normal response times.\n")
	g := build(t, cfg, store)
	first, err := g.Invoke(ctx, document.Source{URI: source})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(source))
	documentID := hex.EncodeToString(sum[:])
	query := func() []string {
		t.Helper()
		result, err := db.Query(ctx, dbCfg.Collection, nil, fmt.Sprintf(`metadata["document_id"] == "%s"`, documentID), []string{"id"}, milvus.WithSearchQueryConsistencyLevel(entity.ClStrong))
		if err != nil {
			t.Fatal("querying visible acceptance IDs:", err)
		}
		column := result.GetColumn("id")
		if column == nil {
			t.Fatal("missing ID column")
		}
		ids := []string{}
		for i := 0; i < column.Len(); i++ {
			id, err := column.GetAsString(i)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return sorted(ids)
	}
	before := query()
	if len(first) == 0 || !reflect.DeepEqual(sorted(first), before) {
		t.Fatal("initial visible primary keys differ")
	}
	repeated, err := g.Invoke(ctx, document.Source{URI: source})
	if err != nil {
		t.Fatal(err)
	}
	after := query()
	if !reflect.DeepEqual(first, repeated) || !reflect.DeepEqual(before, after) {
		t.Fatal("repeat changed visible primary key set")
	}
	t.Logf("retained source=%s; chunks=%d; repeated visible primary key set unchanged", source, len(first))
}
