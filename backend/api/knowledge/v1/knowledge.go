package v1

import (
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/gogf/gf/v2/frame/g"
)

type ListReq struct {
	g.Meta    `path:"/knowledge/documents" method:"get" summary:"知识文档目录"`
	Page      int      `json:"page" d:"1" v:"between:1,1000000"`
	PageSize  int      `json:"pageSize" d:"20" v:"between:1,100"`
	Q         string   `json:"q"`
	DocType   string   `json:"docType"`
	Domain    string   `json:"domain"`
	Service   string   `json:"service"`
	Tags      []string `json:"tags"`
	AlertName string   `json:"alertName"`
}
type DetailReq struct {
	g.Meta `path:"/knowledge/documents/{id}" method:"get" summary:"知识文档详情"`
	ID     string `json:"id" v:"required|regex:^[a-f0-9]{64}$"`
}
type ChunksReq struct {
	g.Meta   `path:"/knowledge/documents/{id}/chunks" method:"get" summary:"当前版本切片"`
	ID       string `json:"id" v:"required|regex:^[a-f0-9]{64}$"`
	Page     int    `json:"page" d:"1" v:"between:1,1000000"`
	PageSize int    `json:"pageSize" d:"20" v:"between:1,100"`
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
	g.Meta        `path:"/knowledge/search" method:"post" summary:"向量、BM25 或混合检索"`
	Query         string            `json:"query" v:"required"`
	Mode          string            `json:"mode"`
	TopK          int               `json:"topK" d:"10" v:"between:1,100"`
	Filters       knowledge.Filters `json:"filters"`
	DenseMinScore *float64          `json:"denseMinScore"`
}
type CreateReq struct {
	g.Meta `path:"/knowledge/documents" method:"post" mime:"multipart/form-data" summary:"上传知识文档"`
}
type UpdateReq struct {
	g.Meta `path:"/knowledge/documents/{id}" method:"put" mime:"multipart/form-data" summary:"更新知识文档"`
	ID     string `json:"id" v:"required|regex:^[a-f0-9]{64}$"`
}

type ListRes knowledge.DocumentPage
type DetailRes knowledge.DocumentInfo
type SearchRes knowledge.SearchResult
type CreateRes knowledge.DocumentInfo
type UpdateRes knowledge.DocumentInfo
