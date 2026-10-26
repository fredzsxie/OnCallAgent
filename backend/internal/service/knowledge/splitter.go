package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func trimUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func Split(source, text, documentHash string, cfg config.Knowledge) ([]*schema.Document, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, ErrInvalidDocument
	}
	documentID := digest(source)
	version := digest(fmt.Sprintf("split-v1:%s:%d:%d", documentHash, cfg.ChunkSize, cfg.ChunkOverlap))
	title := trimUTF8(strings.TrimSuffix(path.Base(source), path.Ext(source)), 1024)
	docs := []*schema.Document{}
	var section strings.Builder
	flush := func() {
		content := section.String()
		for start := 0; start < len(content); {
			end := min(start+cfg.ChunkSize, len(content))
			// Byte limits must never cut a UTF-8 code point, including at overlap boundaries.
			for end < len(content) && !utf8.RuneStart(content[end]) {
				end--
			}
			chunk := content[start:end]
			if strings.TrimSpace(chunk) != "" {
				index := len(docs)
				docs = append(docs, &schema.Document{
					ID: digest(fmt.Sprintf("%s:%s:%d", documentID, version, index)), Content: chunk,
					MetaData: map[string]any{"source": source, "title": title, "chunk_index": index, "content_hash": digest(chunk), "document_hash": documentHash, "document_id": documentID, "version": version},
				})
			}
			if end == len(content) {
				break
			}
			next := end - cfg.ChunkOverlap
			for next < end && !utf8.RuneStart(content[next]) {
				next++
			}
			if next <= start {
				next = end
			}
			start = next
		}
		section.Reset()
	}
	fence := byte(0)
	fenceLength := 0
	text = strings.ReplaceAll(strings.TrimPrefix(text, "\uFEFF"), "\r\n", "\n")
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		run := 0
		if len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
			for run < len(trimmed) && trimmed[run] == trimmed[0] {
				run++
			}
		}
		if fence != 0 {
			if run >= fenceLength && trimmed[0] == fence && strings.TrimSpace(trimmed[run:]) == "" {
				fence = 0
			}
		} else if run >= 3 {
			fence = trimmed[0]
			fenceLength = run
		} else {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level >= 1 && level <= 6 && level < len(trimmed) && (trimmed[level] == ' ' || trimmed[level] == '\t') {
				flush()
				title = trimUTF8(strings.TrimSpace(trimmed[level:]), 1024)
			}
		}
		section.WriteString(line)
	}
	flush()
	if len(docs) == 0 {
		return nil, ErrInvalidDocument
	}
	return docs, nil
}
