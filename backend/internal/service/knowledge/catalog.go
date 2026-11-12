package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("knowledge document not found")
var ErrConflict = errors.New("knowledge source or version conflict")
var ErrInvalidMetadata = errors.New("invalid knowledge metadata")

type Metadata struct {
	Title      string   `json:"title" yaml:"title"`
	DocType    string   `json:"docType" yaml:"docType"`
	Domain     string   `json:"domain" yaml:"domain"`
	Service    string   `json:"service" yaml:"service"`
	Tags       []string `json:"tags" yaml:"tags"`
	Aliases    []string `json:"aliases" yaml:"aliases"`
	AlertNames []string `json:"alertNames" yaml:"alertNames"`
	Owner      string   `json:"owner" yaml:"owner"`
	SourceURL  string   `json:"sourceUrl" yaml:"sourceUrl"`
}

type DocumentInfo struct {
	Metadata
	ID            string `json:"id"`
	Source        string `json:"source"`
	ActiveVersion string `json:"activeVersion"`
	LatestVersion string `json:"latestVersion"`
	UpdatedAt     string `json:"updatedAt"`
	ChunkCount    int    `json:"chunkCount"`
	IndexOnly     bool   `json:"indexOnly"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FilePath      string `json:"-"`
	Content       string `json:"content,omitempty"`
}

type Filters struct {
	DocType   string   `json:"docType"`
	Domain    string   `json:"domain"`
	Service   string   `json:"service"`
	Tags      []string `json:"tags"`
	AlertName string   `json:"alertName"`
}

func (f Filters) Validate() error {
	if f.DocType != "" && f.DocType != "glossary" && f.DocType != "sop" && f.DocType != "reference" {
		return ErrInvalidMetadata
	}
	for _, s := range append([]string{f.Domain, f.Service, f.AlertName}, f.Tags...) {
		if len(s) > 256 || strings.ContainsRune(s, 0) {
			return ErrInvalidMetadata
		}
	}
	if len(f.Tags) > 32 {
		return ErrInvalidMetadata
	}
	return nil
}

type DocumentPage struct {
	Items    []DocumentInfo `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}
type Catalog struct{ db *sql.DB }

func OpenCatalog(filename string) (*Catalog, error) {
	if filename == "" || !filepath.IsAbs(filename) {
		return nil, errors.New("catalog path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(filename); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, ErrInvalidDocument
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	// A single connection keeps PRAGMAs and short catalog transactions consistent.
	db.SetMaxOpenConns(1)
	c := &Catalog{db: db}
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS documents (
 id TEXT PRIMARY KEY, source TEXT NOT NULL UNIQUE, active_version TEXT NOT NULL DEFAULT '', latest_version TEXT NOT NULL, updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS versions (
 document_id TEXT NOT NULL REFERENCES documents(id), version TEXT NOT NULL, metadata TEXT NOT NULL,
 file_path TEXT NOT NULL, index_only INTEGER NOT NULL DEFAULT 0, chunk_count INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', PRIMARY KEY(document_id,version));
 CREATE TABLE IF NOT EXISTS settings (name TEXT PRIMARY KEY, value TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filename, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}
func (c *Catalog) Close() error { return c.db.Close() }

// Bind prevents a catalog from silently validating results from another collection.
func (c *Catalog) Bind(ctx context.Context, profile string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO settings(name,value) VALUES('index_profile',?) ON CONFLICT(name) DO NOTHING`, profile)
	if err != nil {
		return err
	}
	var actual string
	err = c.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE name='index_profile'`).Scan(&actual)
	if err != nil {
		return err
	}
	if actual != profile {
		return errors.New("catalog index profile differs; use a separate catalog and reindex")
	}
	return nil
}

const documentSelect = `SELECT d.id,d.source,d.active_version,d.latest_version,d.updated_at,v.metadata,v.file_path,v.chunk_count,v.index_only,l.state,l.error
 FROM documents d JOIN versions v ON v.document_id=d.id AND v.version=CASE WHEN d.active_version='' THEN d.latest_version ELSE d.active_version END
 JOIN versions l ON l.document_id=d.id AND l.version=d.latest_version`

func scanDocument(row interface{ Scan(...any) error }) (DocumentInfo, error) {
	var d DocumentInfo
	var raw string
	err := row.Scan(&d.ID, &d.Source, &d.ActiveVersion, &d.LatestVersion, &d.UpdatedAt, &raw, &d.FilePath, &d.ChunkCount, &d.IndexOnly, &d.Status, &d.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(raw), &d.Metadata)
	return d, err
}
func (c *Catalog) Get(ctx context.Context, id string) (DocumentInfo, error) {
	return scanDocument(c.db.QueryRowContext(ctx, documentSelect+` WHERE d.id=?`, id))
}
func (c *Catalog) BySource(ctx context.Context, source string) (DocumentInfo, error) {
	return scanDocument(c.db.QueryRowContext(ctx, documentSelect+` WHERE d.source=?`, source))
}

func (c *Catalog) List(ctx context.Context, f Filters, q string, page, size int) (DocumentPage, error) {
	out := DocumentPage{Items: []DocumentInfo{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 100 || len(q) > 1024 {
		return out, ErrInvalidMetadata
	}
	if err := f.Validate(); err != nil {
		return out, err
	}
	where := []string{"1=1"}
	args := []any{}
	for _, x := range []struct{ key, value string }{{"docType", f.DocType}, {"domain", f.Domain}, {"service", f.Service}} {
		if x.value != "" {
			where = append(where, `json_extract(v.metadata,'$.`+x.key+`')=?`)
			args = append(args, x.value)
		}
	}
	for _, tag := range f.Tags {
		where = append(where, `EXISTS(SELECT 1 FROM json_each(v.metadata,'$.tags') WHERE value=?)`)
		args = append(args, tag)
	}
	if f.AlertName != "" {
		where = append(where, `EXISTS(SELECT 1 FROM json_each(v.metadata,'$.alertNames') WHERE value=?)`)
		args = append(args, f.AlertName)
	}
	if q != "" {
		where = append(where, `instr(lower(json_extract(v.metadata,'$.title')),lower(?))>0`)
		args = append(args, q)
	}
	query := documentSelect + ` WHERE ` + strings.Join(where, " AND ")
	if err := c.db.QueryRowContext(ctx, `SELECT count(*) FROM (`+query+`)`, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := c.db.QueryContext(ctx, query+` ORDER BY d.updated_at DESC,d.id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, d)
	}
	return out, rows.Err()
}

func (c *Catalog) Stage(ctx context.Context, source, version string, m Metadata, path string, indexOnly bool, expected *string, createOnly bool) (string, error) {
	id := digest(source)
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var active string
	err = tx.QueryRowContext(ctx, `SELECT active_version FROM documents WHERE id=?`, id).Scan(&active)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if (createOnly && exists) || (expected != nil && (!exists || active != *expected)) {
		return "", ErrConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO documents(id,source,latest_version,updated_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET latest_version=excluded.latest_version,updated_at=excluded.updated_at`, id, source, version, now); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO versions(document_id,version,metadata,file_path,index_only,state) VALUES(?,?,?,?,?,'indexing') ON CONFLICT(document_id,version) DO UPDATE SET state='indexing',error='',file_path=excluded.file_path`, id, version, string(raw), path, indexOnly); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func (c *Catalog) Publish(ctx context.Context, id, version string, count int) error {
	if count < 1 {
		return ErrInvalidDocument
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE documents SET active_version=?,updated_at=? WHERE id=? AND latest_version=?`, version, time.Now().UTC().Format(time.RFC3339Nano), id, version)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE versions SET state='cleanup_pending',chunk_count=?,error='' WHERE document_id=? AND version=?`, count, id, version); err != nil {
		return err
	}
	return tx.Commit()
}
func (c *Catalog) SetState(ctx context.Context, id, version, state, code string) error {
	switch state {
	case "failed", "cleanup_pending", "ready":
	default:
		return ErrInvalidMetadata
	}
	_, err := c.db.ExecContext(ctx, `UPDATE versions SET state=?,error=? WHERE document_id=? AND version=?`, state, code, id, version)
	return err
}
func (c *Catalog) ActiveVersions(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := c.db.QueryContext(ctx, `SELECT id,active_version FROM documents WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, v string
		if err = rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}
func (c *Catalog) Recover(ctx context.Context) error {
	// An interrupted staging attempt never becomes active merely because the process restarted.
	_, err := c.db.ExecContext(ctx, `UPDATE versions SET state='failed',error='interrupted' WHERE state='indexing'`)
	return err
}
func (c *Catalog) Profile(ctx context.Context) (string, error) {
	var s string
	err := c.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE name='index_profile'`).Scan(&s)
	return s, err
}
func catalogError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("knowledge catalog %s: %w", action, err)
}
