package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const payloadLimit = 8 << 10

// responseCapture counts successful writes and retains only a bounded JSON copy.
type responseCapture struct {
	data []byte
	size int64
}

func (p *responseCapture) write(data []byte, contentType string) {
	p.size += int64(len(data))
	if payloadType(contentType) != "json" {
		return
	}
	remaining := payloadLimit + 1 - len(p.data)
	p.data = append(p.data, data[:min(len(data), remaining)]...)
}

func (p *responseCapture) writeString(data, contentType string) {
	p.size += int64(len(data))
	if payloadType(contentType) != "json" {
		return
	}
	remaining := payloadLimit + 1 - len(p.data)
	p.data = append(p.data, data[:min(len(data), remaining)]...)
}

// requestCapture observes business reads; embedding preserves the original Close.
type requestCapture struct {
	io.ReadCloser
	responseCapture
	contentType string
	expected    int64
	eof         bool
	failed      bool
}

func newRequestCapture(r *http.Request) *requestCapture {
	body := r.Body
	if body == nil {
		body = http.NoBody
	}
	return &requestCapture{ReadCloser: body, contentType: r.Header.Get("Content-Type"), expected: r.ContentLength, eof: body == http.NoBody}
}

func (p *requestCapture) Read(data []byte) (int, error) {
	n, err := p.ReadCloser.Read(data)
	p.write(data[:n], p.contentType)
	p.eof = p.eof || err == io.EOF
	p.failed = p.failed || err != nil && err != io.EOF
	return n, err
}

func (p *requestCapture) preview() string {
	complete := !p.failed && (p.eof || p.expected >= 0 && p.size == p.expected)
	return p.responseCapture.preview(p.contentType, complete)
}

func payloadType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "omitted"
	}
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		return "json"
	case mediaType == "text/event-stream":
		return "stream"
	case strings.HasPrefix(mediaType, "multipart/"):
		return "multipart"
	default:
		return "omitted"
	}
}

// Partial or invalid JSON never falls back to raw text, which could contain secrets.
func (p *responseCapture) preview(contentType string, complete bool) string {
	if kind := payloadType(contentType); kind != "json" {
		return "<" + kind + ">"
	}
	if p.size > payloadLimit {
		return "<truncated>"
	}
	if !complete || p.size != int64(len(p.data)) {
		return "<incomplete>"
	}
	if p.size == 0 {
		return "<empty>"
	}
	if !utf8.Valid(p.data) {
		return "<invalid>"
	}
	decoder := json.NewDecoder(bytes.NewReader(p.data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return "<invalid>"
	}
	redactPayload(value)
	data, err := json.Marshal(value)
	if err != nil || len(data) > payloadLimit || escapedPayloadSize(data) > payloadLimit {
		return "<truncated>"
	}
	return string(data)
}

func redactPayload(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
			sensitive := false
			for _, name := range []string{"authorization", "cookie", "password", "passwd", "secret", "token", "apikey", "credential", "privatekey", "accesskey"} {
				if strings.Contains(normalized, name) {
					sensitive = true
					break
				}
			}
			if sensitive {
				v[key] = "[REDACTED]"
			} else {
				redactPayload(item)
			}
		}
	case []any:
		for _, item := range v {
			redactPayload(item)
		}
	}
}

// Bound the rendered field as well; logger escaping can expand Unicode and brackets.
// Returning a marker instead of slicing keeps both JSON and UTF-8 complete.
func escapedPayloadSize(data []byte) int {
	size := 0
	for _, r := range string(data) {
		switch {
		case r == '\\' || r == ']':
			size += 2
		case unicode.IsControl(r) || !unicode.IsPrint(r):
			size += len(strconv.QuoteRuneToGraphic(r)) - 2
		default:
			size += utf8.RuneLen(r)
		}
		if size > payloadLimit {
			break
		}
	}
	return size
}
