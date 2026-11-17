package health

import (
	healthservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/health"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

type ControllerV1 struct{}

func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

func (c *ControllerV1) Health(ctx *gin.Context) {
	middleware.Respond(ctx, healthservice.Health(), nil)
}
