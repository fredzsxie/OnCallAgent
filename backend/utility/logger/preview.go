package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

const PreviewLimit = 8 << 10

// TextPreview bounds the final escaped field without cutting UTF-8 or escape sequences.
func TextPreview(value string) string {
	if len(value) > PreviewLimit {
		return "<truncated>"
	}
	if !utf8.ValidString(value) {
		return "<invalid>"
	}
	if len(escape(value)) > PreviewLimit {
		return "<truncated>"
	}
	return value
}

// JSONPreview shares the same redaction and size policy across HTTP and tool boundaries.
func JSONPreview(data []byte) string {
	if len(data) > PreviewLimit {
		return "<truncated>"
	}
	if len(data) == 0 {
		return "<empty>"
	}
	if !utf8.Valid(data) {
		return "<invalid>"
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return "<invalid>"
	}
	redactPayload(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<invalid>"
	}
	return TextPreview(string(encoded))
}

// URLPreview sanitizes a copy, leaving the actual request and authentication unchanged.
func URLPreview(target *url.URL) string {
	if target == nil {
		return ""
	}
	if target.Opaque != "" {
		return "<omitted>"
	}
	clean := *target
	clean.User = nil
	clean.Fragment, clean.RawFragment = "", ""
	query, err := url.ParseQuery(clean.RawQuery)
	if err != nil {
		clean.RawQuery = "<invalid>"
	} else {
		for key := range query {
			if sensitiveKey(key) {
				query.Set(key, "[REDACTED]")
			}
		}
		clean.RawQuery = query.Encode()
	}
	return TextPreview(clean.String())
}

var keyNormalizer = strings.NewReplacer("-", "", "_", "")

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(keyNormalizer.Replace(key))
	if normalized == "key" {
		return true
	}
	for _, name := range []string{"authorization", "cookie", "password", "passwd", "secret", "token", "apikey", "credential", "privatekey", "accesskey", "signature"} {
		if strings.Contains(normalized, name) {
			return true
		}
	}
	return false
}

func redactPayload(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if sensitiveKey(key) {
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
