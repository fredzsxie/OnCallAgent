package v1

import "github.com/gogf/gf/v2/frame/g"

type HealthReq struct {
	g.Meta `path:"/health" method:"get" summary:"服务存活检查"`
}

type HealthRes struct {
	Status string `json:"status"`
}
