package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

const maxToolResponseBytes = 8 << 20

type requestObservation struct {
	status  atomic.Int32
	invalid atomic.Bool
}
type observationKey struct{}

type toolTransport struct {
	base *http.Transport
	auth config.HTTPAuth
}

func (t *toolTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	switch t.auth.Type {
	case "basic":
		copy.SetBasicAuth(t.auth.Username, t.auth.Password)
	case "bearer":
		copy.Header.Set("Authorization", "Bearer "+t.auth.BearerToken)
	case "header":
		copy.Header.Set(t.auth.HeaderName, t.auth.HeaderValue)
	}
	response, err := t.base.RoundTrip(copy)
	if err != nil {
		return nil, err
	}
	if obs, ok := request.Context().Value(observationKey{}).(*requestObservation); ok {
		obs.status.Store(int32(response.StatusCode))
		kind := mediaType(response.Header.Get("Content-Type"))
		if response.StatusCode == 200 && kind != "application/json" && kind != "text/event-stream" {
			obs.invalid.Store(true)
		}
	}
	// Bound dependency bodies, including SDK error responses, before any JSON decoding.
	response.Body = &limitedToolBody{ReadCloser: response.Body, remaining: maxToolResponseBytes + 1}
	return response, nil
}

func mediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

type limitedToolBody struct {
	io.ReadCloser
	remaining int64
}

func (r *limitedToolBody) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func newToolHTTP(c config.ToolHTTP) (*http.Client, *http.Transport) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: &toolTransport{base, c.Auth}, Timeout: c.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base
}

func observe(ctx context.Context) (context.Context, *requestObservation) {
	obs := &requestObservation{}
	return context.WithValue(ctx, observationKey{}, obs), obs
}
