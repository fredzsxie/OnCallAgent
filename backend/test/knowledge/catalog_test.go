package knowledge_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

func TestCatalogPublicationPersistenceAndConflict(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	c, err := knowledge.OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Bind(ctx, "collection:model:dim"); err != nil {
		t.Fatal(err)
	}
	if err = c.Bind(ctx, "other"); err == nil {
		t.Fatal("profile mismatch accepted")
	}
	m := knowledge.Metadata{Title: "CDN 术语", DocType: "glossary", Tags: []string{"cache"}}
	id, err := c.Stage(ctx, "cdn.md", "v1", m, "/private/tmp/one.md", false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	active, err := c.ActiveVersions(ctx, []string{id})
	if err != nil || active[id] != "" {
		t.Fatal("staging became visible")
	}
	if err = c.Publish(ctx, id, "v1", 2); err != nil {
		t.Fatal(err)
	}
	_, err = c.Stage(ctx, "cdn.md", "v2", m, "/private/tmp/two.md", false, nil, true)
	if !errors.Is(err, knowledge.ErrConflict) {
		t.Fatal("source conflict lost")
	}
	wrong := "wrong"
	_, err = c.Stage(ctx, "cdn.md", "v2", m, "/private/tmp/two.md", false, &wrong, false)
	if !errors.Is(err, knowledge.ErrConflict) {
		t.Fatal("version conflict lost")
	}
	old := "v1"
	_, err = c.Stage(ctx, "cdn.md", "v2", m, "/private/tmp/two.md", false, &old, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := c.Get(ctx, id)
	if err != nil || d.ActiveVersion != "v1" || d.Status != "failed" || d.ChunkCount != 2 {
		t.Fatalf("old version not protected: %+v %v", d, err)
	}
	c.Close()
	c, err = knowledge.OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	page, err := c.List(ctx, knowledge.Filters{DocType: "glossary", Tags: []string{"cache"}}, "术语", 1, 20)
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("catalog query: %+v %v", page, err)
	}
	page, err = c.List(ctx, knowledge.Filters{}, "%", 1, 20)
	if err != nil || page.Total != 0 {
		t.Fatal("title filter treated as SQL pattern")
	}
}
