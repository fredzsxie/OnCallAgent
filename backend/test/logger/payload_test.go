package logger_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fredzsxie/OnCallAgent/backend/internal/router"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gin-gonic/gin"
)

func payloadAPI(t *testing.T) (*gin.Engine, *capture) {
	t.Helper()
	logs := &capture{}
	root, _ := testLogger(t, settings(), logger.Options{Console: logs})
	return router.New(root, router.Dependencies{}), logs
}

func TestHTTPPayloadRedactsNestedJSONAndPreservesWriteString(t *testing.T) {
	engine, logs := payloadAPI(t)
	input := `[{"Authorization":"request-bearer","nested":{"COOKIE":"request-cookie","clientSecret":"client-secret","X-Api-Key":"api-secret","accessToken":"access-secret","private_key":"pem-private-value","accessKey":"access-key-value","safe":"keep"}}]`
	output := `{"result":{"Password":"response-secret","safe":"ok"},"access_token":"response-token"}`
	var received string
	engine.POST("/_payload/redact", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Error(err)
		}
		received = string(body)
		c.Header("Content-Type", "application/problem+json; charset=utf-8")
		_, _ = c.Writer.WriteString(output)
	})
	request := httptest.NewRequest(http.MethodPost, "/_payload/redact", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("X-Request-ID", "edge.Payload_1:retry-2")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if received != input || response.Body.String() != output || response.Header().Get("X-Request-ID") != "edge.Payload_1:retry-2" {
		t.Fatal("payload logging changed HTTP data", received, response.Body.String())
	}
	text := logs.String()
	for _, secret := range []string{"request-bearer", "request-cookie", "client-secret", "api-secret", "access-secret", "pem-private-value", "access-key-value", "response-secret", "response-token"} {
		if strings.Contains(text, secret) {
			t.Fatal("credential leaked", secret)
		}
	}
	for _, want := range []string{`"safe":"keep"`, `"safe":"ok"`, "REDACTED", fmt.Sprintf("req_bytes[%d]", len(input)), fmt.Sprintf("rsp_bytes[%d]", len(output))} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing payload result %q: %s", want, text)
		}
	}
	if strings.Count(text, " req[") != 1 || strings.Count(text, " rsp[") != 1 {
		t.Fatal("payload must belong to one terminal record", text)
	}
}

func TestHTTPPayloadInvalidAndOversizedBodiesUseMarkers(t *testing.T) {
	for _, tc := range []struct{ name, body, marker string }{
		{"invalid", `{"password":"invalid-private-value"`, "<invalid>"},
		{"trailing_data", `{"safe":true} private-trailing-data`, "<invalid>"},
		{"invalid_utf8", `{"safe":"` + string([]byte{0xff}) + `"}`, "<invalid>"},
		{"oversized", `{"safe":"` + strings.Repeat("private-large-value", 600) + `"}`, "<truncated>"},
		{"escaped_size", `{"safe":"` + strings.Repeat("]", 6000) + `"}`, "<truncated>"},
		{"unicode_escaped_size", `{"safe":"` + strings.Repeat("\u0080", 2000) + `"}`, "<truncated>"},
		{"redacted_size", "[" + strings.TrimSuffix(strings.Repeat(`{"token":0},`, 500), ",") + "]", "<truncated>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, logs := payloadAPI(t)
			var received string
			engine.POST("/_payload/marker", func(c *gin.Context) {
				body, err := io.ReadAll(c.Request.Body)
				if err != nil {
					t.Error(err)
				}
				received = string(body)
				c.Header("Content-Type", "application/json")
				_, _ = c.Writer.Write([]byte(tc.body))
			})
			request := httptest.NewRequest(http.MethodPost, "/_payload/marker", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			text := logs.String()
			if received != tc.body || response.Body.String() != tc.body {
				t.Fatal("logging consumed or truncated the business body")
			}
			if !strings.Contains(text, "req["+tc.marker+"]") || !strings.Contains(text, "rsp["+tc.marker+"]") || strings.Contains(text, "private-") || !utf8.ValidString(text) || len(text) > 4096 {
				t.Fatal("unsafe or unbounded payload preview", text)
			}
		})
	}
}

type payloadReadCloser struct {
	io.Reader
	closed bool
}

func (b *payloadReadCloser) Close() error { b.closed = true; return nil }

func TestHTTPPayloadCompletenessAndOriginalClose(t *testing.T) {
	for _, tc := range []struct {
		name, body, preview string
		length              int64
		readAll             bool
	}{
		{"known_length_without_eof", `{}`, `{}`, 2, false},
		{"unknown_length_without_eof", `{}`, `<incomplete>`, -1, false},
		{"partial_known_length", `{"private":"unread-value"}`, `<incomplete>`, 26, false},
		{"unknown_length_with_eof", `{"safe":"whole"}`, `{"safe":"whole"}`, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, logs := payloadAPI(t)
			var received string
			engine.POST("/_payload/read", func(c *gin.Context) {
				defer c.Request.Body.Close()
				var body []byte
				var err error
				if tc.readAll {
					body, err = io.ReadAll(c.Request.Body)
				} else {
					body = make([]byte, 2)
					_, err = io.ReadFull(c.Request.Body, body)
				}
				if err != nil {
					t.Error(err)
				}
				received = string(body)
				c.JSON(http.StatusOK, gin.H{"safe": true})
			})
			original := &payloadReadCloser{Reader: strings.NewReader(tc.body)}
			request := httptest.NewRequest(http.MethodPost, "/_payload/read", nil)
			request.Body, request.ContentLength = original, tc.length
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(httptest.NewRecorder(), request)
			if !original.closed || tc.readAll && received != tc.body || !tc.readAll && received != tc.body[:2] {
				t.Fatal("request wrapper changed reads or Close", original.closed, received)
			}
			if !strings.Contains(logs.String(), "req["+tc.preview+"]") || strings.Contains(logs.String(), "unread-value") {
				t.Fatal("incomplete body was exposed", logs.String())
			}
		})
	}
}

func TestHTTPPayloadSkipsSSEAndMultipartContents(t *testing.T) {
	for _, tc := range []struct{ contentType, marker string }{
		{"text/event-stream; charset=utf-8", "<stream>"},
		{"multipart/form-data; boundary=payload-test", "<multipart>"},
		{"text/plain; charset=utf-8", "<omitted>"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			engine, logs := payloadAPI(t)
			body := strings.Repeat("private-stream-or-upload-body", 600)
			engine.POST("/_payload/skip", func(c *gin.Context) {
				_, _ = io.Copy(io.Discard, c.Request.Body)
				c.Header("Content-Type", tc.contentType)
				_, _ = c.Writer.WriteString(body)
				c.Writer.Flush()
			})
			request := httptest.NewRequest(http.MethodPost, "/_payload/skip", strings.NewReader(body))
			request.Header.Set("Content-Type", tc.contentType)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			text := logs.String()
			if response.Body.String() != body || !strings.Contains(text, "req["+tc.marker+"]") || !strings.Contains(text, "rsp["+tc.marker+"]") || strings.Contains(text, "private-stream-or-upload-body") {
				t.Fatal("stream/upload data was captured or changed", text)
			}
		})
	}
}

type payloadShortWriter struct{ http.ResponseWriter }

type payloadReadError struct{}

func (payloadReadError) Read(data []byte) (int, error) {
	return copy(data, "{}"), io.ErrUnexpectedEOF
}
func (payloadReadError) Close() error { return nil }

func TestHTTPPayloadReadFailureIsIncompleteEvenAtDeclaredLength(t *testing.T) {
	engine, logs := payloadAPI(t)
	engine.POST("/_payload/read_error", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if string(body) != "{}" || err != io.ErrUnexpectedEOF {
			t.Error("request read error was changed", string(body), err)
		}
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request"})
	})
	request := httptest.NewRequest(http.MethodPost, "/_payload/read_error", nil)
	request.Body, request.ContentLength = payloadReadError{}, 2
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if !strings.Contains(logs.String(), "req[<incomplete>]") {
		t.Fatal("failed read was logged as a complete body", logs.String())
	}
}

func TestHTTPPayloadMultipartSkipsJSONResponse(t *testing.T) {
	engine, logs := payloadAPI(t)
	engine.POST("/_payload/upload_response", func(c *gin.Context) {
		_, _ = io.Copy(io.Discard, c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"filePath": "private-storage-path", "content": "private-upload-text"})
	})
	request := httptest.NewRequest(http.MethodPost, "/_payload/upload_response", strings.NewReader("private-file"))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=payload-test")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if !strings.Contains(logs.String(), "rsp[<multipart>]") || strings.Contains(logs.String(), "private-") {
		t.Fatal("upload response body was logged", logs.String())
	}
}

func (w payloadShortWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data[:min(4, len(data))])
	if err != nil {
		return n, err
	}
	return n, io.ErrClosedPipe
}

func (w payloadShortWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func TestHTTPPayloadShortWriteNeverPretendsResponseIsComplete(t *testing.T) {
	engine, logs := payloadAPI(t)
	engine.GET("/_payload/short", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		_, _ = c.Writer.WriteString("trueEXTRA")
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(payloadShortWriter{response}, httptest.NewRequest(http.MethodGet, "/_payload/short", nil))
	if response.Body.String() != "true" || !strings.Contains(logs.String(), "rsp_bytes[4]") || !strings.Contains(logs.String(), "rsp[<incomplete>]") {
		t.Fatal("failed write was logged as a complete body", logs.String())
	}
}

func TestHTTPPayloadRequestIDAndPanicHaveOneTerminalResult(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, tc := range []struct {
		id    string
		panic bool
	}{{"request.preserved:1", false}, {"bad]request", false}, {"request.panic:1", true}} {
		t.Run(tc.id, func(t *testing.T) {
			engine, logs := payloadAPI(t)
			var observedID string
			engine.GET("/_payload/id", func(c *gin.Context) {
				observedID = c.GetHeader("X-Request-ID")
				if tc.panic {
					panic("private-panic-value")
				}
				c.JSON(http.StatusOK, gin.H{"safe": true})
			})
			request := httptest.NewRequest(http.MethodGet, "/_payload/id", nil)
			request.Header.Set("X-Request-ID", tc.id)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			id := response.Header().Get("X-Request-ID")
			if id == "" || id != observedID || tc.id == "bad]request" && !uuid.MatchString(id) || tc.id != "bad]request" && id != tc.id {
				t.Fatal("request ID was not preserved or replaced safely", id, observedID)
			}
			text := requestLines(logs.String(), id)
			if strings.Count(text, "请求完成")+strings.Count(text, "请求未完成") != 1 || strings.Contains(logs.String(), "private-panic-value") {
				t.Fatal("missing or duplicated terminal result", logs.String())
			}
			if tc.panic && (response.Code != 500 || !strings.Contains(text, "outcome[failed]")) {
				t.Fatal("panic did not produce a failed terminal result", response.Code, text)
			}
		})
	}
}
