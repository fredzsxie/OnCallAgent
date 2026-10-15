package health

import (
	"context"

	v1 "github.com/fredzsxie/OnCallAgent/backend/api/health/v1"
	logic "github.com/fredzsxie/OnCallAgent/backend/internal/logic/health"
)

type ControllerV1 struct{}

func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

func (c *ControllerV1) Health(_ context.Context, _ *v1.HealthReq) (*v1.HealthRes, error) {
	return logic.Health(), nil
}
