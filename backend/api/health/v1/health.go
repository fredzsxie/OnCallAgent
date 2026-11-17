package v1

type HealthReq struct{}

type HealthRes struct {
	Status string `json:"status"`
}
