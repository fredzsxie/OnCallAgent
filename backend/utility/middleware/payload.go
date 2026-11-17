package middleware

import (
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

const payloadLimit = logger.PreviewLimit

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
	return logger.JSONPreview(p.data)
}
