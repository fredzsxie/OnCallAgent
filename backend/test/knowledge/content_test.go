package knowledge_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

func TestTypedContentMetadataAndVersion(t *testing.T) {
	cfg := ingestionConfig(t)
	cfg.ChunkSize = 2000
	raw := []byte("---\ntitle: 回源\ndocType: glossary\naliases: [Origin, origin]\n---\n# 回源\n从源站获取内容。\n")
	a, err := knowledge.Prepare("origin.md", raw, knowledge.MetadataPatch{}, cfg, "model:3")
	if err != nil {
		t.Fatal(err)
	}
	if a.Metadata.DocType != "glossary" || len(a.Chunks) != 1 || !strings.Contains(a.Chunks[0].MetaData["search_text"].(string), "Origin") {
		t.Fatal("term context missing")
	}
	empty := []string{}
	b, err := knowledge.Prepare("origin.md", raw, knowledge.MetadataPatch{Aliases: &empty}, cfg, "model:3")
	if err != nil || b.Version == a.Version {
		t.Fatal("metadata change did not change version")
	}
	c, _ := knowledge.Prepare("origin.md", raw, knowledge.MetadataPatch{}, cfg, "other-model:3")
	if c.Version == a.Version {
		t.Fatal("profile change did not change version")
	}
	if _, err = knowledge.Prepare("origin.md", []byte("---\nunknown: true\n---\nbody"), knowledge.MetadataPatch{}, cfg, "m"); err == nil {
		t.Fatal("unknown metadata accepted")
	}
}
func TestTypedSOPSectionsAndOversizedBlocks(t *testing.T) {
	cfg := ingestionConfig(t)
	cfg.ChunkSize = 160
	cfg.ChunkOverlap = 20
	text := "# 排查\n\n## 检查\n\n```sh\n# not a heading\ncurl -I example.test\n```\n\n" + strings.Repeat("回源超时需检查链路。", 50)
	p, err := knowledge.Prepare("sop.md", []byte(text), knowledge.MetadataPatch{}, cfg, "m")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Chunks {
		if !utf8.ValidString(c.Content) || len(c.Content) > cfg.ChunkSize {
			t.Fatal("invalid chunk")
		}
		if strings.Contains(c.Content, "curl") && !strings.Contains(c.Content, "```sh") {
			t.Fatal("small code block was split")
		}
		if strings.Contains(c.MetaData["section_path"].(string), "not a heading") {
			t.Fatal("code heading changed section")
		}
	}
}
