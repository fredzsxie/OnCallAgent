package v1

import "github.com/gogf/gf/v2/frame/g"

type ChatReq struct {
	g.Meta   `path:"/chat" method:"post" summary:"普通聊天"`
	Id       string `json:"Id" v:"required#Id is required"`
	Question string `json:"Question" v:"required#Question is required"`
}

type ChatRes struct {
	Answer string `json:"answer"`
}
