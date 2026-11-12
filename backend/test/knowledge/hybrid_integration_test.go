package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/joho/godotenv"
)

type acceptanceCorpus struct {
	Documents []struct {
		Source   string             `json:"source"`
		Metadata knowledge.Metadata `json:"metadata"`
		Body     string             `json:"body"`
	} `json:"documents"`
	Queries []struct {
		ID              string   `json:"id"`
		Query           string   `json:"query"`
		RelevantSources []string `json:"relevantSources"`
	} `json:"queries"`
}

func loadCorpus(t *testing.T) acceptanceCorpus {
	t.Helper()
	raw, err := os.ReadFile("testdata/retrieval-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var c acceptanceCorpus
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestAcceptanceCorpusHasThirtyLabeledQueries(t *testing.T) {
	c := loadCorpus(t)
	if len(c.Queries) < 30 || len(c.Documents) < 10 {
		t.Fatal("insufficient evaluation corpus")
	}
	sources := map[string]bool{}
	for _, d := range c.Documents {
		sources[d.Source] = true
	}
	seen := map[string]bool{}
	for _, q := range c.Queries {
		if seen[q.ID] || strings.TrimSpace(q.Query) == "" || len(q.RelevantSources) == 0 {
			t.Fatal("invalid labels")
		}
		seen[q.ID] = true
		for _, s := range q.RelevantSources {
			if !sources[s] {
				t.Fatal("unknown relevance target")
			}
		}
	}
}

type retrievalMetrics struct {
	Mode      string  `json:"mode"`
	Queries   int     `json:"queries"`
	RecallAt5 float64 `json:"recallAt5"`
	MRR       float64 `json:"mrr"`
	P95Ms     int64   `json:"p95Ms"`
}

func TestHybridRetrievalIntegration(t *testing.T) {
	if os.Getenv("ONCALL_INTEGRATION") != "1" {
		t.Skip("explicit real Milvus and embedding acceptance; creates isolated collections")
	}
	envFile := os.Getenv("ONCALL_KNOWLEDGE_INTEGRATION_ENV")
	if envFile == "" {
		envFile = "../../.env"
	}
	values, err := godotenv.Read(envFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cannot load integration environment")
	}
	for name, value := range values {
		if _, set := os.LookupEnv(name); !set {
			t.Setenv(name, value)
		}
	}
	ec, err := config.LoadEmbedding()
	if err != nil {
		t.Fatal(err)
	}
	mc, err := config.LoadMilvus()
	if err != nil {
		t.Fatal(err)
	}
	unique, err := knowledge.NewID()
	if err != nil {
		t.Fatal(err)
	}
	mc.Collection = "knowledge_v2_acceptance_" + unique
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	emb, err := client.NewEmbedding(ctx, ec)
	if err != nil {
		t.Fatal(err)
	}
	defer emb.Close()
	db, err := client.NewMilvus(ctx, mc)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := db.GetVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated collection=%s server=%s sdk=2.5.2 model=%s", mc.Collection, version, ec.Model)
	if _, err = knowledge.InitV2(ctx, db, emb, mc); err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.NewStore(ctx, db, emb, mc)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	kc := config.Knowledge{FileDir: root, MaxUploadBytes: 1 << 20, ChunkSize: 2000, ChunkOverlap: 200, BatchSize: 16}
	catalog, err := knowledge.OpenCatalog(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	manager, err := knowledge.NewManager(ctx, kc, catalog, store, store.Profile(ec.Model))
	if err != nil {
		t.Fatal(err)
	}
	search, err := knowledge.NewSearchService(catalog, store, knowledge.SearchConfig{Mode: "hybrid", TopK: 3, DenseMinScore: .5, Timeout: mc.Timeout})
	if err != nil {
		t.Fatal(err)
	}
	corpus := loadCorpus(t)
	originals := map[string]string{}
	published := map[string]knowledge.DocumentInfo{}
	for _, doc := range corpus.Documents {
		meta, _ := json.Marshal(doc.Metadata)
		raw := "---\n" + string(meta) + "\n---\n" + doc.Body
		originals[doc.Source] = raw
		d, err := manager.Ingest(ctx, doc.Source, strings.NewReader(raw), knowledge.MetadataPatch{}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		published[doc.Source] = d
	}
	metrics := []retrievalMetrics{}
	for _, mode := range []string{"dense", "bm25", "hybrid"} {
		m := retrievalMetrics{Mode: mode, Queries: len(corpus.Queries)}
		latencies := []int64{}
		for _, q := range corpus.Queries {
			result, err := search.Search(ctx, knowledge.SearchRequest{Query: q.Query, Mode: mode, TopK: 5})
			if err != nil {
				t.Fatalf("mode=%s query=%s: %v", mode, q.ID, err)
			}
			relevant := map[string]bool{}
			for _, s := range q.RelevantSources {
				relevant[s] = true
			}
			found := map[string]bool{}
			rank := 0
			for i, h := range result.Documents {
				if relevant[h.Source] {
					found[h.Source] = true
					if rank == 0 {
						rank = i + 1
					}
				}
			}
			m.RecallAt5 += float64(len(found)) / float64(len(relevant))
			if rank > 0 {
				m.MRR += 1 / float64(rank)
			}
			latencies = append(latencies, result.DurationMs)
		}
		m.RecallAt5 /= float64(m.Queries)
		m.MRR /= float64(m.Queries)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		m.P95Ms = latencies[(len(latencies)*95+99)/100-1]
		metrics = append(metrics, m)
		raw, _ := json.Marshal(m)
		t.Log(string(raw))
	}
	if metrics[2].RecallAt5 < metrics[0].RecallAt5 {
		t.Error("hybrid Recall@5 regressed below dense baseline")
	}
	empty, err := search.Search(ctx, knowledge.SearchRequest{Query: "zzknowledgeacceptancenoterm99999", Mode: "bm25", TopK: 5})
	if err != nil || len(empty.Documents) != 0 {
		t.Fatal("BM25 empty-result behavior differs")
	}
	filtered, err := search.Search(ctx, knowledge.SearchRequest{Query: "TLS handshake", Mode: "hybrid", TopK: 5, Filters: knowledge.Filters{DocType: "sop", Service: "edge", Tags: []string{"acceptance"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Documents) == 0 {
		t.Fatal("metadata filters lost target")
	}
	for _, h := range filtered.Documents {
		if h.DocType != "sop" {
			t.Fatal("type filter leaked")
		}
	}
	old := published["origin.md"]
	updated, err := manager.Ingest(ctx, "origin.md", strings.NewReader(originals["origin.md"]+"\n新增验收标记 knowledge-v2-update。\n"), knowledge.MetadataPatch{}, &old.ActiveVersion, false)
	if err != nil || updated.ActiveVersion == old.ActiveVersion {
		t.Fatal("update did not publish")
	}
	result, err := search.Search(ctx, knowledge.SearchRequest{Query: "knowledge-v2-update", Mode: "bm25", TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range result.Documents {
		if h.DocumentID == old.ID && h.Version != updated.ActiveVersion {
			t.Fatal("old version leaked")
		}
	}
	adapter, err := retriever.NewMilvusRetriever(search, config.Retrieval{Mode: "hybrid", TopK: 3, ScoreThreshold: .5})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := adapter.Search(ctx, "CDN_ORIGIN_TIMEOUT")
	if err != nil || len(evidence.Documents) == 0 || evidence.ScoreType != "rrf" {
		t.Fatal("Agent adapter did not use hybrid evidence")
	}
	// Exercise migration against a second isolated legacy collection; production collections are never selected.
	legacyCfg := mc
	legacyCfg.Collection = "knowledge_v1_acceptance_" + unique
	if _, err = knowledge.Init(ctx, db, emb, legacyCfg); err != nil {
		t.Fatal(err)
	}
	legacyStore, err := knowledge.NewStore(ctx, db, emb, legacyCfg)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := knowledge.Split("migration-check.md", "# Migration\nHistorical CDN acceptance evidence.", strings.Repeat("a", 64), kc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = knowledge.IndexChunks(ctx, legacyStore, docs, 16); err != nil {
		t.Fatal(err)
	}
	plan, err := knowledge.InspectLegacy(ctx, db, legacyCfg.Collection, root, nil)
	if err != nil || plan.Conflicts != 0 {
		t.Fatal("legacy preflight failed")
	}
	if _, err = knowledge.ApplyMigration(ctx, manager, plan); err != nil {
		t.Fatal(err)
	}
	if _, err = knowledge.ApplyMigration(ctx, manager, plan); err != nil {
		t.Fatal("migration repeat failed", err)
	}
	report := struct {
		Server            string             `json:"server"`
		Collection        string             `json:"collection"`
		LegacyCollection  string             `json:"legacyCollection"`
		Model             string             `json:"model"`
		Metrics           []retrievalMetrics `json:"metrics"`
		MigrationVerified bool               `json:"migrationVerified"`
	}{version, mc.Collection, legacyCfg.Collection, ec.Model, metrics, true}
	raw, _ := json.MarshalIndent(report, "", "  ")
	t.Log(string(raw))
	if output := os.Getenv("ONCALL_KNOWLEDGE_ACCEPTANCE_REPORT"); output != "" {
		if err = os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
