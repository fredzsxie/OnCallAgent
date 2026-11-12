package testutil

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

// PublicationMemory exercises controllers and catalog publication, not Milvus ranking quality.
type PublicationMemory struct{ MemoryStore }

func (s *PublicationMemory) Chunks(ctx context.Context, id, version string) ([]*schema.Document, error) {
	docs := []*schema.Document{}
	for _, d := range s.Snapshot() {
		if d.MetaData["document_id"] == id && d.MetaData["version"] == version {
			docs = append(docs, cloneDocument(d))
		}
	}
	sort.Slice(docs, func(i, j int) bool { return chunkNumber(docs[i]) < chunkNumber(docs[j]) })
	return docs, ctx.Err()
}
func (s *PublicationMemory) Verify(ctx context.Context, id, version string, ids []string) error {
	docs, err := s.Chunks(ctx, id, version)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for _, d := range docs {
		set[d.ID] = true
	}
	if len(set) != len(ids) {
		return errors.New("incomplete memory publication")
	}
	for _, id := range ids {
		if !set[id] {
			return errors.New("unexpected memory publication ID")
		}
	}
	return nil
}
func (s *PublicationMemory) Candidates(ctx context.Context, q, mode string, k int, f knowledge.Filters) ([]*schema.Document, error) {
	docs := []*schema.Document{}
	query := strings.ToLower(strings.TrimSpace(q))
	for _, original := range s.Snapshot() {
		d := cloneDocument(original)
		str := func(key string) string { v, _ := d.MetaData[key].(string); return v }
		if f.DocType != "" && str("docType") != f.DocType || f.Domain != "" && str("domain") != f.Domain || f.Service != "" && str("service") != f.Service {
			continue
		}
		matches := true
		for _, tag := range f.Tags {
			if !containsMetadata(d.MetaData["tags"], tag) {
				matches = false
			}
		}
		if f.AlertName != "" && !containsMetadata(d.MetaData["alertNames"], f.AlertName) {
			matches = false
		}
		if !matches {
			continue
		}
		text := strings.ToLower(str("search_text"))
		if text == "" {
			text = strings.ToLower(d.Content)
		}
		exact := strings.Contains(text, query)
		if mode == knowledge.BM25 && !exact {
			continue
		}
		score := 0.6
		if exact {
			score = 0.9
		}
		if mode == knowledge.Hybrid {
			score /= 30
		}
		if mode == knowledge.BM25 {
			score = 2
		}
		docs = append(docs, d.WithScore(score))
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Score() == docs[j].Score() {
			return docs[i].ID < docs[j].ID
		}
		return docs[i].Score() > docs[j].Score()
	})
	return docs[:min(k, len(docs))], ctx.Err()
}
func cloneDocument(d *schema.Document) *schema.Document {
	meta := map[string]any{}
	for k, v := range d.MetaData {
		meta[k] = v
	}
	return &schema.Document{ID: d.ID, Content: d.Content, MetaData: meta}
}
func chunkNumber(d *schema.Document) int {
	switch n := d.MetaData["chunk_index"].(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
func containsMetadata(value any, want string) bool {
	switch values := value.(type) {
	case []string:
		for _, v := range values {
			if v == want {
				return true
			}
		}
	case []any:
		for _, v := range values {
			if v == want {
				return true
			}
		}
	}
	return false
}
