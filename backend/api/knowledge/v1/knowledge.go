package v1

import (
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
)

type ListReq struct {
	Page      int      `json:"page" form:"page" binding:"min=1,max=1000000"`
	PageSize  int      `json:"pageSize" form:"pageSize" binding:"min=1,max=100"`
	Q         string   `json:"q" form:"q"`
	DocType   string   `json:"docType" form:"docType"`
	Domain    string   `json:"domain" form:"domain"`
	Service   string   `json:"service" form:"service"`
	Tags      []string `json:"tags" form:"tags[]"`
	AlertName string   `json:"alertName" form:"alertName"`
}
type DetailReq struct {
	ID string `json:"id" uri:"id"`
}
type ChunksReq struct {
	ID       string `json:"id" uri:"id"`
	Page     int    `json:"page" form:"page" binding:"min=1,max=1000000"`
	PageSize int    `json:"pageSize" form:"pageSize" binding:"min=1,max=100"`
}
type Chunk struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	SectionPath string `json:"sectionPath"`
	Index       int    `json:"index"`
}
type ChunksRes struct {
	Items    []Chunk `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
}
type SearchReq struct {
	Query         string            `json:"query" binding:"required"`
	Mode          string            `json:"mode"`
	TopK          int               `json:"topK" binding:"min=1,max=100"`
	Filters       knowledge.Filters `json:"filters"`
	DenseMinScore *float64          `json:"denseMinScore"`
}
type CreateReq struct{}
type UpdateReq struct {
	ID string `json:"id" uri:"id"`
}

type ListRes knowledge.DocumentPage
type DetailRes knowledge.DocumentInfo
type SearchRes knowledge.SearchResult
type CreateRes knowledge.DocumentInfo
type UpdateRes knowledge.DocumentInfo
