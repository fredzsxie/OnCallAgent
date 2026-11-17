package middleware

import (
	"errors"
	"net/http"

	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/gin-gonic/gin"
)

// BodyLimit bounds every request, including bodies with unknown or chunked lengths.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			Respond(c, nil, knowledge.ErrTooLarge)
			c.Abort()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// BindJSON reads the bounded body completely so trailing whitespace cannot bypass the limit.
func BindJSON(c *gin.Context, target any) error {
	err := c.ShouldBindBodyWithJSON(target)
	var tooLarge *http.MaxBytesError
	if err == nil || errors.As(err, &tooLarge) {
		return err
	}
	return ErrInvalidRequest
}
