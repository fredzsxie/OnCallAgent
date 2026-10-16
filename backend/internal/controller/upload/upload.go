package upload

import (
	"context"
	"errors"

	v1 "github.com/fredzsxie/OnCallAgent/backend/api/upload/v1"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/gogf/gf/v2/frame/g"
)

type ControllerV1 struct{ service *knowledge.KnowledgeService }

func NewV1(service *knowledge.KnowledgeService) *ControllerV1 { return &ControllerV1{service} }

func (c *ControllerV1) Upload(ctx context.Context, _ *v1.UploadReq) (res *v1.UploadRes, err error) {
	file := g.RequestFromCtx(ctx).GetUploadFile("file")
	if file == nil {
		return nil, knowledge.ErrInvalidDocument
	}
	input, err := file.Open()
	if err != nil {
		return nil, errors.Join(knowledge.ErrSave, knowledge.ErrRetryable, err)
	}
	defer func() {
		if closeErr := input.Close(); closeErr != nil {
			err = errors.Join(err, knowledge.ErrSave, knowledge.ErrRetryable, closeErr)
		}
	}()
	result, err := c.service.Ingest(ctx, knowledge.UploadName(file.Filename), input)
	if err != nil {
		return nil, err
	}
	return &v1.UploadRes{FileName: result.FileName, FilePath: result.FilePath, FileSize: result.FileSize, Source: result.Source, ContentHash: result.ContentHash, ChunkCount: result.ChunkCount}, nil
}
