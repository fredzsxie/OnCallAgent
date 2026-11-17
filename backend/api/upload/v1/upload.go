package v1

type UploadReq struct{}

type UploadRes struct {
	FileName    string `json:"fileName"`
	FilePath    string `json:"filePath"`
	FileSize    int64  `json:"fileSize"`
	Source      string `json:"source"`
	ContentHash string `json:"contentHash"`
	ChunkCount  int    `json:"chunkCount"`
}
