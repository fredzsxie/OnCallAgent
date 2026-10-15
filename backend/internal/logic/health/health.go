package health

import v1 "github.com/fredzsxie/OnCallAgent/backend/api/health/v1"

func Health() *v1.HealthRes {
	return &v1.HealthRes{Status: "alive"}
}
