package middleware

import (
	"errors"
	"mime"
	"net/http"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

const MultipartOverhead int64 = 1 << 20

func UploadBody(maxBytes int64) ghttp.HandlerFunc {
	return func(r *ghttp.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			r.SetError(knowledge.ErrInvalidDocument)
			return
		}
		if mediaType != "multipart/form-data" {
			r.SetError(knowledge.ErrUnsupportedFormat)
			return
		}
		limit := maxBytes + MultipartOverhead
		if r.ContentLength > limit {
			r.SetError(knowledge.ErrTooLarge)
			return
		}
		// Parse before GoFrame binds the request, so body errors keep their HTTP meaning.
		r.Body = http.MaxBytesReader(r.Response.RawWriter(), r.Body, limit)
		err = r.Request.ParseMultipartForm(min(maxBytes, 1<<20))
		defer func() {
			if r.MultipartForm != nil {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					g.Log().Warning(r.Context(), "could not remove multipart temporary files")
				}
			}
		}()
		if err != nil {
			var tooLarge *http.MaxBytesError
			switch {
			case errors.As(err, &tooLarge):
				r.SetError(knowledge.ErrTooLarge)
			case r.Context().Err() != nil:
				r.SetError(r.Context().Err())
			default:
				r.SetError(knowledge.ErrInvalidDocument)
			}
			return
		}
		count := 0
		for _, files := range r.MultipartForm.File {
			count += len(files)
		}
		files := r.MultipartForm.File["file"]
		if count != 1 || len(files) != 1 {
			r.SetError(knowledge.ErrInvalidDocument)
			return
		}
		if files[0].Size > maxBytes {
			r.SetError(knowledge.ErrTooLarge)
			return
		}
		r.Middleware.Next()
	}
}
