package logger_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	"github.com/fredzsxie/OnCallAgent/backend/internal/router"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

func TestHTTPBodyLimitRejectsOversizedJSONBeforeExecution(t *testing.T) {
	const maxUploadBytes int64 = 32
	const limit = maxUploadBytes + middleware.MultipartOverhead
	for _, tc := range []struct{ path, validBody string }{
		{"/api/chat", `{"Id":"limit","Question":"hello"}`},
		{"/api/chat_stream", `{"Id":"limit","Question":"hello"}`},
		{"/api/knowledge/search", `{"query":"hello"}`},
		{"/api/ai_ops", `{}`},
		{"/api/ai_ops_stream", `{}`},
	} {
		for _, chunked := range []bool{false, true} {
			for _, trailingSpace := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/chunked=%t/trailing_space=%t", tc.path, chunked, trailingSpace), func(t *testing.T) {
					var logs capture
					root, _ := testLogger(t, settings(), logger.Options{Console: &logs})
					// Nil dependencies make accidental model or search execution fail the 413 assertion.
					engine := router.New(root, router.Dependencies{MaxUploadBytes: maxUploadBytes})
					body := `{"Question":"` + strings.Repeat("private-large-body", int(limit)/18+1) + `"}`
					if trailingSpace {
						body = tc.validBody + strings.Repeat(" ", int(limit)-len(tc.validBody)+1)
					}
					request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					if chunked {
						request.ContentLength = -1
						request.TransferEncoding = []string{"chunked"}
					}
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, request)
					if response.Code != http.StatusRequestEntityTooLarge || !json.Valid(response.Body.Bytes()) || strings.Contains(response.Body.String(), "event:") {
						t.Fatalf("oversized request reached execution: status=%d body=%s", response.Code, response.Body.String())
					}
					id := response.Header().Get("X-Request-ID")
					text := requestLines(logs.String(), id)
					if id == "" || strings.Count(text, "请求未完成") != 1 || !strings.Contains(text, "code[413]") || strings.Contains(text, "请求处理发生异常") || strings.Contains(text, "private-large-body") {
						t.Fatal("size rejection lost tracing or exposed request contents", text)
					}
				})
			}
		}
	}
}

func TestHTTPBodyLimitAcceptsBoundaryAndDefault(t *testing.T) {
	for _, tc := range []struct {
		name           string
		maxUploadBytes int64
		bodyBytes      int
	}{
		{"at_limit", 32, int(32 + middleware.MultipartOverhead)},
		{"below_limit", 32, int(31 + middleware.MultipartOverhead)},
		{"default_upload_size", 0, 2 << 20},
	} {
		for _, chunked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/chunked=%t", tc.name, chunked), func(t *testing.T) {
				var logs capture
				root, _ := testLogger(t, settings(), logger.Options{Console: &logs})
				engine := router.New(root, router.Dependencies{MaxUploadBytes: tc.maxUploadBytes})
				var received string
				engine.POST("/_body_limit", func(c *gin.Context) {
					var request v1.ChatReq
					if err := middleware.BindJSON(c, &request); err != nil {
						middleware.Respond(c, nil, err)
						return
					}
					received = request.Question
					middleware.Respond(c, len(received), nil)
				})
				const prefix, suffix = `{"Id":"limit","Question":"`, `"}`
				question := strings.Repeat("x", tc.bodyBytes-len(prefix)-len(suffix))
				body := prefix + question + suffix
				request := httptest.NewRequest(http.MethodPost, "/_body_limit", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				if chunked {
					request.ContentLength = -1
					request.TransferEncoding = []string{"chunked"}
				}
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusOK || received != question {
					t.Fatalf("body within the limit was rejected or changed: status=%d received=%d want=%d", response.Code, len(received), len(question))
				}
			})
		}
	}
}
