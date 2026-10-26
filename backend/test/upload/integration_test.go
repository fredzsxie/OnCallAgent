package upload_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/test/testutil"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
	milvus "github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

type failingBatch struct {
	knowledge.DocumentStore
	calls, failAt int
}

func (s *failingBatch) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	s.calls++
	ids, err := s.DocumentStore.Store(ctx, docs, opts...)
	if err != nil {
		return nil, err
	}
	if s.calls == s.failAt {
		return nil, errors.New("injected lost batch acknowledgement")
	}
	return ids, nil
}

func TestUploadIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("set ONCALL_INTEGRATION=1 to upload real embeddings and retain stage04_2 records")
	}
	values, err := godotenv.Read(filepath.Join("..", "..", ".env"))
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
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	store, err := knowledge.NewStore(ctx, db, emb, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	injected := &failingBatch{DocumentStore: store}
	cfg := config.Knowledge{FileDir: t.TempDir(), MaxUploadBytes: 4096, ChunkSize: 96, ChunkOverlap: 12, BatchSize: 2}
	service, err := testutil.NewKnowledgeService(cfg, injected)
	if err != nil {
		t.Fatal(err)
	}
	api := startAPI(t, service, cfg.MaxUploadBytes)
	unique, err := knowledge.NewID()
	if err != nil {
		t.Fatal(err)
	}
	source := "stage04_2_acceptance_" + unique + ".md"
	docSum := sha256.Sum256([]byte(source))
	docID := hex.EncodeToString(docSum[:])
	query := func() (ids []string, versions map[string]bool) {
		t.Helper()
		columns, err := db.Query(ctx, dbCfg.Collection, nil, fmt.Sprintf(`metadata["document_id"] == "%s"`, docID), []string{"id", "metadata"}, milvus.WithSearchQueryConsistencyLevel(entity.ClStrong))
		if err != nil {
			t.Fatal("querying acceptance chunks:", err)
		}
		versions = map[string]bool{}
		idColumn, metadata := columns.GetColumn("id"), columns.GetColumn("metadata")
		if idColumn == nil || metadata == nil {
			t.Fatal("missing query fields")
		}
		for i := 0; i < idColumn.Len(); i++ {
			id, err := idColumn.GetAsString(i)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := metadata.GetAsString(i)
			if err != nil {
				t.Fatal(err)
			}
			var meta map[string]any
			if err = json.Unmarshal([]byte(raw), &meta); err != nil {
				t.Fatal(err)
			}
			if meta["source"] != source || meta["document_id"] != docID {
				t.Fatal("metadata differs")
			}
			versions[meta["version"].(string)] = true
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return
	}
	text := "# Stage04 acceptance\n" + strings.Repeat("Knowledge upload 中文验收。 ", 7)
	body := uploadFile(t, api, source, text, 200)
	var result knowledge.Result
	if err = json.Unmarshal(body.Data, &result); err != nil {
		t.Fatal(err)
	}
	first, oldVersions := query()
	if len(first) != result.ChunkCount || len(oldVersions) != 1 {
		t.Fatal("first upload chunk count/version differs")
	}
	uploadFile(t, api, source, text, 200)
	repeated, _ := query()
	if !reflect.DeepEqual(first, repeated) {
		t.Fatal("repeat upload changed ID set")
	}
	updated := "# Updated acceptance\n" + strings.Repeat("New content 更新。 ", 10)
	injected.failAt = injected.calls + 2
	uploadFile(t, api, source, updated, 503)
	partial, _ := query()
	for _, id := range first {
		if i := sort.SearchStrings(partial, id); i == len(partial) || partial[i] != id {
			t.Fatal("batch failure removed old data")
		}
	}
	uploadFile(t, api, source, updated, 200)
	final, newVersions := query()
	expected, err := knowledge.Split(source, updated, func() string { sum := sha256.Sum256([]byte(updated)); return hex.EncodeToString(sum[:]) }(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(expected))
	for i, doc := range expected {
		want[i] = doc.ID
	}
	sort.Strings(want)
	if !reflect.DeepEqual(final, want) || len(newVersions) != 1 {
		t.Fatal("retry did not converge to expected chunk IDs")
	}
	for version := range oldVersions {
		if newVersions[version] {
			t.Fatal("old version survived successful update")
		}
	}
	uploadFile(t, api, source, updated, 200)
	last, _ := query()
	if !reflect.DeepEqual(final, last) {
		t.Fatal("updated repeat changed IDs")
	}
	t.Logf("retained source=%s; initial chunks=%d; final chunks=%d; repeat IDs and failure retry verified", source, len(first), len(final))
}
