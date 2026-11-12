package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"gopkg.in/yaml.v3"
)

// Pointer fields distinguish an omitted override from an explicitly cleared value.
type MetadataPatch struct {
	Title      *string   `json:"title"`
	DocType    *string   `json:"docType"`
	Domain     *string   `json:"domain"`
	Service    *string   `json:"service"`
	Tags       *[]string `json:"tags"`
	Aliases    *[]string `json:"aliases"`
	AlertNames *[]string `json:"alertNames"`
	Owner      *string   `json:"owner"`
	SourceURL  *string   `json:"sourceUrl"`
}
type Prepared struct {
	Metadata Metadata
	Body     string
	Version  string
	Chunks   []*schema.Document
}

func (m Metadata) Normalize(source string) (Metadata, error) {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		m.Title = strings.TrimSuffix(path.Base(source), path.Ext(source))
	}
	if m.DocType == "" {
		m.DocType = "reference"
	}
	if err := (Filters{DocType: m.DocType}).Validate(); err != nil {
		return m, err
	}
	for _, v := range []string{m.Title, m.Domain, m.Service, m.Owner, m.SourceURL} {
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) || len(v) > 1024 {
			return m, ErrInvalidMetadata
		}
	}
	if len(m.Domain) > 256 || len(m.Service) > 256 {
		return m, ErrInvalidMetadata
	}
	for _, list := range []*[]string{&m.Tags, &m.Aliases, &m.AlertNames} {
		if len(*list) > 32 {
			return m, ErrInvalidMetadata
		}
		values := []string{}
		for _, s := range *list {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if !utf8.ValidString(s) || strings.ContainsRune(s, 0) || len(s) > 256 {
				return m, ErrInvalidMetadata
			}
			values = append(values, s)
		}
		sort.Strings(values)
		out := []string{}
		for _, v := range values {
			if len(out) == 0 || out[len(out)-1] != v {
				out = append(out, v)
			}
		}
		*list = out
	}
	if m.SourceURL != "" {
		u, err := url.Parse(m.SourceURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return m, ErrInvalidMetadata
		}
	}
	return m, nil
}
func applyPatch(m Metadata, p MetadataPatch) Metadata {
	if p.Title != nil {
		m.Title = *p.Title
	}
	if p.DocType != nil {
		m.DocType = *p.DocType
	}
	if p.Domain != nil {
		m.Domain = *p.Domain
	}
	if p.Service != nil {
		m.Service = *p.Service
	}
	if p.Tags != nil {
		m.Tags = *p.Tags
	}
	if p.Aliases != nil {
		m.Aliases = *p.Aliases
	}
	if p.AlertNames != nil {
		m.AlertNames = *p.AlertNames
	}
	if p.Owner != nil {
		m.Owner = *p.Owner
	}
	if p.SourceURL != nil {
		m.SourceURL = *p.SourceURL
	}
	return m
}
func Prepare(source string, raw []byte, patch MetadataPatch, cfg config.Knowledge, profile string) (Prepared, error) {
	var out Prepared
	if err := cfg.Validate(); err != nil {
		return out, err
	}
	source, err := NormalizeSource(source)
	if err != nil {
		return out, err
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return out, ErrInvalidDocument
	}
	body := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\uFEFF"), "\r\n", "\n")
	m := Metadata{}
	if strings.HasPrefix(body, "---\n") {
		end := strings.Index(body[4:], "\n---\n")
		if end < 0 || end > 16384 {
			return out, ErrInvalidMetadata
		}
		decoder := yaml.NewDecoder(strings.NewReader(body[4 : 4+end]))
		decoder.KnownFields(true)
		if err = decoder.Decode(&m); err != nil {
			return out, ErrInvalidMetadata
		}
		body = body[4+end+5:]
	}
	if strings.TrimSpace(body) == "" {
		return out, ErrInvalidDocument
	}
	m, err = applyPatch(m, patch).Normalize(source)
	if err != nil {
		return out, err
	}
	meta, _ := json.Marshal(m)
	// Metadata and the indexing profile are part of identity: aliases and model changes require reindexing too.
	version := digest(fmt.Sprintf("split-v2:%s:%s:%s:%d:%d", digest(body), meta, profile, cfg.ChunkSize, cfg.ChunkOverlap))
	chunks, err := typedChunks(source, body, m, version, cfg)
	if err != nil {
		return out, err
	}
	return Prepared{Metadata: m, Body: body, Version: version, Chunks: chunks}, nil
}

type markdownSection struct{ text, path string }

func sections(body string) []markdownSection {
	out := []markdownSection{}
	var current strings.Builder
	headings := []string{}
	sectionPath := ""
	fence := byte(0)
	fenceLen := 0
	flush := func() {
		if strings.TrimSpace(current.String()) != "" {
			out = append(out, markdownSection{current.String(), sectionPath})
		}
		current.Reset()
	}
	for _, line := range strings.SplitAfter(body, "\n") {
		trimmed := strings.TrimSpace(line)
		run := 0
		if len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
			for run < len(trimmed) && trimmed[run] == trimmed[0] {
				run++
			}
		}
		if fence != 0 {
			if run >= fenceLen && trimmed[0] == fence && strings.TrimSpace(trimmed[run:]) == "" {
				fence = 0
			}
		} else if run >= 3 {
			fence = trimmed[0]
			fenceLen = run
		} else {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level > 0 && level <= 6 && level < len(trimmed) && (trimmed[level] == ' ' || trimmed[level] == '\t') {
				flush()
				for len(headings) < level {
					headings = append(headings, "")
				}
				headings = headings[:level]
				headings[level-1] = strings.TrimSpace(trimmed[level:])
				nonempty := []string{}
				for _, h := range headings {
					if h != "" {
						nonempty = append(nonempty, h)
					}
				}
				sectionPath = strings.Join(nonempty, " / ")
			}
		}
		current.WriteString(line)
	}
	flush()
	return out
}
func textBlocks(text string) []string {
	blocks := []string{}
	var b strings.Builder
	fence := byte(0)
	n := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		t := strings.TrimSpace(line)
		run := 0
		if len(t) > 0 && (t[0] == '`' || t[0] == '~') {
			for run < len(t) && t[run] == t[0] {
				run++
			}
		}
		if fence != 0 {
			if run >= n && t[0] == fence && strings.TrimSpace(t[run:]) == "" {
				fence = 0
			}
		} else if run >= 3 {
			fence = t[0]
			n = run
		}
		b.WriteString(line)
		if t == "" && fence == 0 {
			blocks = append(blocks, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		blocks = append(blocks, b.String())
	}
	return blocks
}
func boundedPieces(text string, size, overlap int) []string {
	out := []string{}
	for start := 0; start < len(text); {
		end := min(start+size, len(text))
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		if end <= start {
			return nil
		}
		out = append(out, text[start:end])
		if end == len(text) {
			break
		}
		next := end - min(overlap, size/2)
		for next < end && !utf8.RuneStart(text[next]) {
			next++
		}
		start = next
	}
	return out
}
func typedChunks(source, body string, m Metadata, version string, cfg config.Knowledge) ([]*schema.Document, error) {
	out := []*schema.Document{}
	documentID := digest(source)
	ss := sections(body)
	if m.DocType == "glossary" && len(body) <= cfg.ChunkSize {
		ss = []markdownSection{{body, m.Title}}
	}
	for _, section := range ss {
		prefix := strings.Join([]string{m.Title, strings.Join(m.Aliases, " "), strings.Join(m.AlertNames, " "), section.path}, "\n") + "\n"
		size := min(cfg.ChunkSize, ContentMaxLength-len(prefix))
		if size < 4 {
			return nil, ErrInvalidMetadata
		}
		pieces := []string{}
		current := ""
		// Prefer whole paragraphs, lists and fenced blocks; only oversized blocks use byte-safe windows.
		for _, block := range textBlocks(section.text) {
			if len(current)+len(block) <= size {
				current += block
				continue
			}
			if strings.TrimSpace(current) != "" {
				pieces = append(pieces, current)
			}
			current = ""
			if len(block) > size {
				pieces = append(pieces, boundedPieces(block, size, cfg.ChunkOverlap)...)
			} else {
				current = block
			}
		}
		if strings.TrimSpace(current) != "" {
			pieces = append(pieces, current)
		}
		for _, piece := range pieces {
			if strings.TrimSpace(piece) == "" {
				continue
			}
			i := len(out)
			raw, _ := json.Marshal(m)
			meta := map[string]any{}
			_ = json.Unmarshal(raw, &meta)
			meta["source"] = source
			meta["document_id"] = documentID
			meta["version"] = version
			meta["chunk_index"] = i
			meta["section_path"] = section.path
			meta["search_text"] = prefix + piece
			meta["content_hash"] = digest(piece)
			meta["document_hash"] = digest(body)
			out = append(out, &schema.Document{ID: digest(fmt.Sprintf("%s:%s:%d", documentID, version, i)), Content: piece, MetaData: meta})
		}
	}
	if len(out) == 0 {
		return nil, ErrInvalidDocument
	}
	return out, nil
}
