package knowledge

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type PublicationStore interface {
	DocumentStore
	Verify(context.Context, string, string, []string) error
	Chunks(context.Context, string, string) ([]*schema.Document, error)
}
type Manager struct {
	cfg     config.Knowledge
	catalog *Catalog
	store   PublicationStore
	profile string
}

func NewManager(ctx context.Context, cfg config.Knowledge, c *Catalog, s PublicationStore, profile string) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.FileDir, 0700); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(cfg.FileDir)
	if err != nil {
		return nil, err
	}
	cfg.FileDir = root
	if c == nil || s == nil || profile == "" {
		return nil, ErrInvalidDocument
	}
	if err := c.Bind(ctx, profile); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, catalog: c, store: s, profile: profile}, nil
}
func (m *Manager) Catalog() *Catalog { return m.catalog }
func (m *Manager) Ingest(ctx context.Context, source string, input io.Reader, patch MetadataPatch, expected *string, createOnly bool) (out DocumentInfo, err error) {
	source, err = NormalizeSource(source)
	if err != nil {
		return out, err
	}
	unlock, err := LockSource(ctx, source)
	if err != nil {
		return out, err
	}
	defer unlock()
	data, err := ReadDocument(ctx, input, m.cfg.MaxUploadBytes)
	if err != nil {
		return out, err
	}
	p, err := Prepare(source, data, patch, m.cfg, m.profile)
	if err != nil {
		return out, err
	}
	if len(p.Chunks) > 16384 {
		return out, ErrTooLarge
	}
	current, getErr := m.catalog.BySource(ctx, source)
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return out, getErr
	}
	// A failed first upload reserves its source but has no published version; allow the same attempt to retry.
	if createOnly && getErr == nil && current.ActiveVersion == "" && current.LatestVersion == p.Version && current.Status == "failed" {
		createOnly = false
	}
	if (createOnly && getErr == nil) || (expected != nil && (getErr != nil || current.ActiveVersion != *expected)) {
		return out, ErrConflict
	}
	if getErr == nil && current.ActiveVersion == p.Version {
		m.cleanup(ctx, current.ID, p.Version)
		return m.catalog.Get(ctx, current.ID)
	}
	filename, _, err := (&KnowledgeService{cfg: m.cfg}).save(digest(source)+"_"+digest(string(data))+strings.ToLower(filepath.Ext(source)), data)
	if err != nil {
		return out, errors.Join(ErrSave, err)
	}
	id, err := m.catalog.Stage(ctx, source, p.Version, p.Metadata, filename, false, expected, createOnly)
	if err != nil {
		return out, err
	}
	published := false
	defer func() {
		if err != nil && !published {
			recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = m.catalog.SetState(recordCtx, id, p.Version, "failed", "index_failed")
		}
	}()
	if err = m.write(ctx, id, p.Version, p.Chunks); err != nil {
		return out, errors.Join(ErrIngest, ErrRetryable, err)
	}
	// Publishing is the visibility boundary; old chunks remain usable until this transaction commits.
	if err = m.catalog.Publish(ctx, id, p.Version, len(p.Chunks)); err != nil {
		return out, errors.Join(ErrIngest, err)
	}
	published = true
	m.cleanup(ctx, id, p.Version)
	return m.catalog.Get(ctx, id)
}
func (m *Manager) write(ctx context.Context, id, version string, docs []*schema.Document) error {
	ids := make([]string, 0, len(docs))
	for start := 0; start < len(docs); start += m.cfg.BatchSize {
		batch := docs[start:min(start+m.cfg.BatchSize, len(docs))]
		written, err := m.store.Store(ctx, batch)
		if err != nil {
			return err
		}
		if len(written) != len(batch) {
			return ErrRetryable
		}
		for i, d := range batch {
			if written[i] != d.ID {
				return ErrRetryable
			}
		}
		ids = append(ids, written...)
	}
	return m.store.Verify(ctx, id, version, ids)
}
func (m *Manager) cleanup(ctx context.Context, id, version string) {
	// Cleanup is recoverable housekeeping; failure must not roll back a published version.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := m.store.DeleteOld(work, id, version); err != nil {
		_ = m.catalog.SetState(work, id, version, "cleanup_pending", "cleanup_failed")
		logger.Failure(ctx, "Knowledge", "旧版本清理待重试", err)
		return
	}
	_ = m.catalog.SetState(work, id, version, "ready", "")
}
func (m *Manager) Recover(ctx context.Context) error {
	if err := m.catalog.Recover(ctx); err != nil {
		return err
	}
	rows, err := m.catalog.db.QueryContext(ctx, `SELECT d.id,d.active_version FROM documents d JOIN versions v ON v.document_id=d.id AND v.version=d.active_version WHERE v.state='cleanup_pending'`)
	if err != nil {
		return err
	}
	pending := [][2]string{}
	for rows.Next() {
		var p [2]string
		if err = rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pending {
		m.cleanup(ctx, p[0], p[1])
	}
	return nil
}
func (m *Manager) Detail(ctx context.Context, id string) (DocumentInfo, error) {
	d, err := m.catalog.Get(ctx, id)
	if err != nil || d.IndexOnly {
		return d, err
	}
	rel, err := filepath.Rel(m.cfg.FileDir, d.FilePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return d, ErrInvalidDocument
	}
	file, err := os.Open(d.FilePath)
	if err != nil {
		return d, err
	}
	defer file.Close()
	raw, err := ReadDocument(ctx, file, m.cfg.MaxUploadBytes)
	if err != nil {
		return d, err
	}
	body := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\uFEFF"), "\r\n", "\n")
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---\n"); end >= 0 {
			body = body[4+end+5:]
		}
	}
	d.Content = body
	return d, nil
}
func (m *Manager) Chunks(ctx context.Context, id string) ([]*schema.Document, error) {
	d, err := m.catalog.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.ActiveVersion == "" {
		return []*schema.Document{}, nil
	}
	return m.store.Chunks(ctx, id, d.ActiveVersion)
}
func (s *Store) Chunks(ctx context.Context, id, version string) ([]*schema.Document, error) {
	if !validHash(id) || !validHash(version) {
		return nil, ErrInvalidDocument
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	data, err := s.db.Query(ctx, s.cfg.Collection, `metadata["document_id"] == "`+id+`" and metadata["version"] == "`+version+`"`, []string{"id", "content", "metadata"}, 16384, 0)
	if err != nil {
		return nil, operationError(ctx, "query chunks", err)
	}
	ids, contents, metas := client.Column(data, "id"), client.Column(data, "content"), client.Column(data, "metadata")
	if ids == nil || contents == nil || metas == nil {
		return nil, errors.New("incomplete chunk query")
	}
	docs := make([]*schema.Document, 0, ids.Len())
	for i := 0; i < ids.Len(); i++ {
		d, err := decodeDocument(ids, contents, metas, i)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool { return chunkIndex(docs[i]) < chunkIndex(docs[j]) })
	return docs, nil
}
func chunkIndex(d *schema.Document) int {
	switch n := d.MetaData["chunk_index"].(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
func (s *Store) Verify(ctx context.Context, id, version string, expected []string) error {
	docs, err := s.Chunks(ctx, id, version)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for _, d := range docs {
		set[d.ID] = true
	}
	if len(set) != len(expected) {
		return errors.New("indexed version is not fully visible")
	}
	for _, id := range expected {
		if !set[id] {
			return errors.New("indexed version IDs differ")
		}
	}
	return nil
}
