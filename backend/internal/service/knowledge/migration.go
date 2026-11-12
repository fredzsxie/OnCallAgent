package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/milvus-io/milvus/client/v2/column"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

type LegacyReader interface {
	Query(context.Context, string, string, []string, int, int) (milvusclient.DataSet, error)
}
type MigrationDocument struct {
	Source           string   `json:"source"`
	LegacyVersion    string   `json:"legacyVersion,omitempty"`
	ChunkCount       int      `json:"chunkCount"`
	IndexOnly        bool     `json:"indexOnly"`
	ConflictVersions []string `json:"conflictVersions,omitempty"`
	chunks           []*schema.Document
	raw              []byte
}
type MigrationPlan struct {
	SnapshotHash string              `json:"snapshotHash"`
	SourceChunks int                 `json:"sourceChunks"`
	Documents    []MigrationDocument `json:"documents"`
	Conflicts    int                 `json:"conflicts"`
}

// InspectLegacy recursively bisects primary-key ranges, avoiding the SDK's 16,384-row offset window.
// Every split covers the entire parent range; unordered query results cannot cause skipped IDs.
func InspectLegacy(ctx context.Context, db LegacyReader, collection, root string, choices map[string]string) (MigrationPlan, error) {
	out := MigrationPlan{Documents: []MigrationDocument{}}
	type bounds struct{ lower, upper *string }
	pending := []bounds{{}}
	all := map[string]*schema.Document{}
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		r := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		terms := []string{}
		if r.lower != nil {
			terms = append(terms, "id >= "+quote(*r.lower))
		}
		if r.upper != nil {
			terms = append(terms, "id < "+quote(*r.upper))
		}
		data, err := db.Query(ctx, collection, strings.Join(terms, " and "), []string{"id", "content", "metadata"}, 512, 0)
		if err != nil {
			return out, err
		}
		if data.Len() == 0 {
			continue
		}
		ids, content, meta := columnByName(data, "id"), columnByName(data, "content"), columnByName(data, "metadata")
		if ids == nil || content == nil || meta == nil {
			return out, ErrInvalidDocument
		}
		batch := make([]*schema.Document, 0, ids.Len())
		for i := 0; i < ids.Len(); i++ {
			d, err := decodeDocument(ids, content, meta, i)
			if err != nil {
				return out, err
			}
			batch = append(batch, d)
		}
		if len(batch) == 512 {
			sort.Slice(batch, func(i, j int) bool { return batch[i].ID < batch[j].ID })
			pivot := batch[len(batch)/2].ID
			if pivot == batch[0].ID {
				return out, errors.New("legacy query returned duplicate primary keys")
			}
			pending = append(pending, bounds{lower: &pivot, upper: r.upper}, bounds{lower: r.lower, upper: &pivot})
			continue
		}
		for _, d := range batch {
			if _, duplicate := all[d.ID]; duplicate {
				return out, errors.New("legacy snapshot contains duplicate IDs")
			}
			all[d.ID] = d
		}
	}
	groups := map[string]map[string][]*schema.Document{}
	ordered := make([]string, 0, len(all))
	for id := range all {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	snapshot := strings.Builder{}
	for _, id := range ordered {
		d := all[id]
		raw, _ := json.Marshal(d.MetaData)
		snapshot.WriteString(id + ":" + digest(d.Content) + ":" + string(raw) + "\n")
		source, _ := d.MetaData["source"].(string)
		if source == "" {
			source = "legacy/" + digest(id) + ".md"
		}

		normalized, err := NormalizeSource(source)
		if err != nil {
			// Early direct inserts used labels instead of filenames; preserve provenance without interpreting them as paths.
			d.MetaData["legacy_source"] = source
			normalized = "legacy/" + digest(source) + ".md"
		}
		source = normalized
		version, _ := d.MetaData["version"].(string)
		if version == "" {
			version = digest("legacy:" + source)
		}
		if groups[source] == nil {
			groups[source] = map[string][]*schema.Document{}
		}
		groups[source][version] = append(groups[source][version], d)
	}
	out.SourceChunks = len(all)
	out.SnapshotHash = digest(snapshot.String())
	sources := []string{}
	for source := range groups {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		versions := groups[source]
		selected := choices[source]
		if selected != "" && versions[selected] == nil {
			return out, errors.New("selected legacy version does not exist")
		}
		if selected == "" && len(versions) > 1 {
			v := []string{}
			for version := range versions {
				v = append(v, version)
			}
			sort.Strings(v)
			out.Documents = append(out.Documents, MigrationDocument{Source: source, ConflictVersions: v})
			out.Conflicts++
			continue
		}
		if selected == "" {
			for v := range versions {
				selected = v
			}
		}
		chunks := versions[selected]
		sort.SliceStable(chunks, func(i, j int) bool {
			a, b := chunkIndex(chunks[i]), chunkIndex(chunks[j])
			if a == b {
				return chunks[i].ID < chunks[j].ID
			}
			return a < b
		})
		raw := findOriginal(root, source, chunks)
		out.Documents = append(out.Documents, MigrationDocument{Source: source, LegacyVersion: selected, ChunkCount: len(chunks), IndexOnly: raw == nil, chunks: chunks, raw: raw})
	}
	return out, nil
}
func columnByName(data milvusclient.DataSet, name string) column.Column {
	for _, c := range data {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
func findOriginal(root, source string, chunks []*schema.Document) []byte {
	if len(chunks) == 0 {
		return nil
	}
	expected, _ := chunks[0].MetaData["document_hash"].(string)
	if !validHash(expected) {
		return nil
	}
	for _, d := range chunks {
		if d.MetaData["document_hash"] != expected {
			return nil
		}
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	for _, name := range []string{digest(source) + "_" + expected + strings.ToLower(filepath.Ext(source)), source} {
		candidate, err := filepath.EvalSymlinks(filepath.Join(canonical, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(canonical, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		raw, err := os.ReadFile(candidate)
		if err == nil && digest(string(raw)) == expected {
			return raw
		}
	}
	return nil
}
func ApplyMigration(ctx context.Context, m *Manager, plan MigrationPlan) (completed int, err error) {
	if plan.Conflicts > 0 {
		return 0, ErrConflict
	}
	for _, doc := range plan.Documents {
		if err = ctx.Err(); err != nil {
			return completed, err
		}
		if doc.IndexOnly {
			_, err = m.importChunks(ctx, doc.Source, doc.chunks)
		} else {
			reference := "reference"
			_, err = m.Ingest(ctx, doc.Source, strings.NewReader(string(doc.raw)), MetadataPatch{DocType: &reference}, nil, false)
		}
		if err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}
func (m *Manager) importChunks(ctx context.Context, source string, original []*schema.Document) (out DocumentInfo, err error) {
	if len(original) == 0 || len(original) > 16384 {
		return out, ErrInvalidDocument
	}
	unlock, err := LockSource(ctx, source)
	if err != nil {
		return out, err
	}
	defer unlock()
	metadata, err := (Metadata{DocType: "reference"}).Normalize(source)
	if err != nil {
		return out, err
	}
	encoded, _ := json.Marshal(original)
	version := digest("legacy-v2:" + m.profile + ":" + string(encoded))
	id := digest(source)
	if d, e := m.catalog.BySource(ctx, source); e == nil && d.ActiveVersion == version {
		m.cleanup(ctx, id, version)
		return m.catalog.Get(ctx, id)
	} else if e != nil && !errors.Is(e, ErrNotFound) {
		return out, e
	}
	chunks := make([]*schema.Document, 0, len(original))
	for i, d := range original {
		raw, _ := json.Marshal(d.MetaData)
		meta := map[string]any{}
		if err = json.Unmarshal(raw, &meta); err != nil {
			return out, err
		}
		meta["source"] = source
		meta["document_id"] = id
		meta["version"] = version
		meta["chunk_index"] = i
		meta["docType"] = "reference"
		meta["title"] = metadata.Title
		meta["tags"] = []string{}
		meta["alertNames"] = []string{}
		meta["search_text"] = metadata.Title + "\n" + d.Content
		chunks = append(chunks, &schema.Document{ID: digest(fmt.Sprintf("%s:%s:%d", id, version, i)), Content: d.Content, MetaData: meta})
	}
	if _, err = m.catalog.Stage(ctx, source, version, metadata, "", true, nil, false); err != nil {
		return out, err
	}
	if err = m.write(ctx, id, version, chunks); err != nil {
		record, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = m.catalog.SetState(record, id, version, "failed", "migration_failed")
		return out, errors.Join(ErrIngest, err)
	}
	if err = m.catalog.Publish(ctx, id, version, len(chunks)); err != nil {
		return out, err
	}
	m.cleanup(ctx, id, version)
	return m.catalog.Get(ctx, id)
}
