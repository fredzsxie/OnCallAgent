package v1

import (
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/gogf/gf/v2/frame/g"
)

type ChatReq struct {
	g.Meta   `path:"/chat" method:"post" summary:"普通聊天"`
	Id       string `json:"Id" v:"required#Id is required"`
	Question string `json:"Question" v:"required#Question is required"`
}

type ChatRes struct {
	Answer string `json:"answer"`
}

type ChatStreamReq struct {
	g.Meta   `path:"/chat_stream" method:"post" summary:"流式聊天"`
	Id       string `json:"Id" v:"required#Id is required"`
	Question string `json:"Question" v:"required#Question is required"`
}
type ChatStreamRes struct{}

type AIOpsReq struct {
	g.Meta `path:"/ai_ops" method:"post" summary:"运维分析"`
}
type AIOpsRes struct {
	Result string           `json:"result"`
	Detail []pipeline.Event `json:"detail"`
}
