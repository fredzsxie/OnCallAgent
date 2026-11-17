package v1

import (
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
)

type ChatReq struct {
	Id       string `json:"Id" binding:"required"`
	Question string `json:"Question" binding:"required"`
}

type ChatRes struct {
	Answer string `json:"answer"`
}

type ChatStreamReq struct {
	Id       string `json:"Id" binding:"required"`
	Question string `json:"Question" binding:"required"`
}
type ChatStreamRes struct{}

type AIOpsReq struct{}
type AIOpsRes struct {
	Result string           `json:"result"`
	Detail []pipeline.Event `json:"detail"`
}

type AIOpsStreamReq struct{}
type AIOpsStreamRes struct{}
