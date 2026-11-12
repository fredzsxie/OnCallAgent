package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	v1 "github.com/fredzsxie/OnCallAgent/backend/api/knowledge/v1"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/gogf/gf/v2/frame/g"
)

type ControllerV1 struct {
	manager *knowledge.Manager
	search  *knowledge.SearchService
}
type WriteControllerV1 struct{ manager *knowledge.Manager }

func NewV1(m *knowledge.Manager, s *knowledge.SearchService) *ControllerV1 {
	return &ControllerV1{m, s}
}
func NewWriteV1(m *knowledge.Manager) *WriteControllerV1 { return &WriteControllerV1{m} }
func (c *ControllerV1) List(ctx context.Context, r *v1.ListReq) (*v1.ListRes, error) {
	p, err := c.manager.Catalog().List(ctx, knowledge.Filters{DocType: r.DocType, Domain: r.Domain, Service: r.Service, Tags: r.Tags, AlertName: r.AlertName}, r.Q, r.Page, r.PageSize)
	return (*v1.ListRes)(&p), err
}
func (c *ControllerV1) Detail(ctx context.Context, r *v1.DetailReq) (*v1.DetailRes, error) {
	d, err := c.manager.Detail(ctx, r.ID)
	return (*v1.DetailRes)(&d), err
}
func (c *ControllerV1) Chunks(ctx context.Context, r *v1.ChunksReq) (*v1.ChunksRes, error) {
	docs, err := c.manager.Chunks(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	out := &v1.ChunksRes{Items: []v1.Chunk{}, Total: len(docs), Page: r.Page, PageSize: r.PageSize}
	start := min(len(docs), (r.Page-1)*r.PageSize)
	end := min(len(docs), start+r.PageSize)
	for _, d := range docs[start:end] {
		section, _ := d.MetaData["section_path"].(string)
		out.Items = append(out.Items, v1.Chunk{ID: d.ID, Content: d.Content, SectionPath: section, Index: start})
		start++
	}
	return out, nil
}
func (c *ControllerV1) Search(ctx context.Context, r *v1.SearchReq) (*v1.SearchRes, error) {
	result, err := c.search.Search(ctx, knowledge.SearchRequest{Query: r.Query, Mode: r.Mode, TopK: r.TopK, Filters: r.Filters, DenseMinScore: r.DenseMinScore})
	return (*v1.SearchRes)(&result), err
}
func (c *WriteControllerV1) Create(ctx context.Context, _ *v1.CreateReq) (*v1.CreateRes, error) {
	d, err := c.ingest(ctx, "", true)
	return (*v1.CreateRes)(d), err
}
func (c *WriteControllerV1) Update(ctx context.Context, r *v1.UpdateReq) (*v1.UpdateRes, error) {
	d, err := c.ingest(ctx, r.ID, false)
	return (*v1.UpdateRes)(d), err
}
func (c *WriteControllerV1) ingest(ctx context.Context, id string, create bool) (*knowledge.DocumentInfo, error) {
	req := g.RequestFromCtx(ctx)
	file := req.GetUploadFile("file")
	if file == nil {
		return nil, knowledge.ErrInvalidDocument
	}
	patch := knowledge.MetadataPatch{}
	raw := req.GetForm("metadata").String()
	if len(raw) > 32768 {
		return nil, knowledge.ErrInvalidMetadata
	}
	if raw != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&patch) != nil {
			return nil, knowledge.ErrInvalidMetadata
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, knowledge.ErrInvalidMetadata
		}
	}
	source := req.GetForm("source").String()
	var expected *string
	if create {
		if source == "" {
			source = knowledge.UploadName(file.Filename)
		}
	} else {
		doc, err := c.manager.Catalog().Get(ctx, id)
		if err != nil {
			return nil, err
		}
		source = doc.Source
		version := req.GetForm("expectedVersion").String()
		if version == "" {
			return nil, knowledge.ErrInvalidMetadata
		}
		expected = &version
	}
	input, err := file.Open()
	if err != nil {
		return nil, errors.Join(knowledge.ErrSave, err)
	}
	defer input.Close()
	d, err := c.manager.Ingest(ctx, source, input, patch, expected, create)
	return &d, err
}
