package v1

import "github.com/gogf/gf/v2/frame/g"

type UploadReq struct {
	g.Meta `path:"/upload" method:"post" mime:"multipart/form-data" summary:"知识文件上传并入库"`
}

type UploadRes struct {
	FileName    string `json:"fileName"`
	FilePath    string `json:"filePath"`
	FileSize    int64  `json:"fileSize"`
	Source      string `json:"source"`
	ContentHash string `json:"contentHash"`
	ChunkCount  int    `json:"chunkCount"`
}
