package middleware

import (
	"errors"
	"mime"
	"net/http"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gin-gonic/gin"
)

const MultipartOverhead int64 = 1 << 20

func UploadBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		reject := func(err error) { Respond(c, nil, err); c.Abort() }
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			reject(knowledge.ErrInvalidDocument)
			return
		}
		if mediaType != "multipart/form-data" {
			reject(knowledge.ErrUnsupportedFormat)
			return
		}
		limit := maxBytes + MultipartOverhead
		if r.ContentLength > limit {
			reject(knowledge.ErrTooLarge)
			return
		}
		// Parse before binding so size, cancellation and malformed-body errors retain their meaning.
		r.Body = http.MaxBytesReader(c.Writer, r.Body, limit)
		err = r.ParseMultipartForm(min(maxBytes, 1<<20))
		defer func() {
			if r.MultipartForm != nil {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					logger.Warn(r.Context(), "Upload", "临时文件清理失败", logger.F("error_type", logger.ErrorType(err)))
				}
			}
		}()
		if err != nil {
			var tooLarge *http.MaxBytesError
			switch {
			case errors.As(err, &tooLarge):
				reject(knowledge.ErrTooLarge)
			case r.Context().Err() != nil:
				reject(r.Context().Err())
			default:
				reject(knowledge.ErrInvalidDocument)
			}
			return
		}
		count := 0
		for _, files := range r.MultipartForm.File {
			count += len(files)
		}
		files := r.MultipartForm.File["file"]
		if count != 1 || len(files) != 1 {
			reject(knowledge.ErrInvalidDocument)
			return
		}
		if files[0].Size > maxBytes {
			reject(knowledge.ErrTooLarge)
			return
		}
		c.Next()
	}
}
