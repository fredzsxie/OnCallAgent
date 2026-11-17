package knowledge

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	v1 "github.com/fredzsxie/OnCallAgent/backend/api/knowledge/v1"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

type ControllerV1 struct {
	manager *knowledge.Manager
	search  *knowledge.SearchService
}
type WriteControllerV1 struct{ manager *knowledge.Manager }

var documentID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func NewV1(m *knowledge.Manager, s *knowledge.SearchService) *ControllerV1 {
	return &ControllerV1{m, s}
}
func NewWriteV1(m *knowledge.Manager) *WriteControllerV1 { return &WriteControllerV1{m} }
func (c *ControllerV1) List(request *gin.Context) {
	r := v1.ListReq{Page: 1, PageSize: 20}
	if request.ShouldBindQuery(&r) != nil {
		middleware.Respond(request, nil, middleware.ErrInvalidRequest)
		return
	}
	// Accept both browser FormData-style arrays and repeated query parameters.
	if r.Tags == nil {
		r.Tags = request.QueryArray("tags")
	}
	p, err := c.manager.Catalog().List(request.Request.Context(), knowledge.Filters{DocType: r.DocType, Domain: r.Domain, Service: r.Service, Tags: r.Tags, AlertName: r.AlertName}, r.Q, r.Page, r.PageSize)
	middleware.Respond(request, (*v1.ListRes)(&p), err)
}
func (c *ControllerV1) Detail(request *gin.Context) {
	if !validDocumentID(request) {
		return
	}
	d, err := c.manager.Detail(request.Request.Context(), request.Param("id"))
	middleware.Respond(request, (*v1.DetailRes)(&d), err)
}
func (c *ControllerV1) Chunks(request *gin.Context) {
	if !validDocumentID(request) {
		return
	}
	r := v1.ChunksReq{Page: 1, PageSize: 20}
	if request.ShouldBindQuery(&r) != nil {
		middleware.Respond(request, nil, middleware.ErrInvalidRequest)
		return
	}
	docs, err := c.manager.Chunks(request.Request.Context(), request.Param("id"))
	if err != nil {
		middleware.Respond(request, nil, err)
		return
	}
	out := &v1.ChunksRes{Items: []v1.Chunk{}, Total: len(docs), Page: r.Page, PageSize: r.PageSize}
	start := min(len(docs), (r.Page-1)*r.PageSize)
	end := min(len(docs), start+r.PageSize)
	for _, d := range docs[start:end] {
		section, _ := d.MetaData["section_path"].(string)
		out.Items = append(out.Items, v1.Chunk{ID: d.ID, Content: d.Content, SectionPath: section, Index: start})
		start++
	}
	middleware.Respond(request, out, nil)
}
func (c *ControllerV1) Search(request *gin.Context) {
	r := v1.SearchReq{TopK: 10}
	if request.ShouldBindJSON(&r) != nil {
		middleware.Respond(request, nil, middleware.ErrInvalidRequest)
		return
	}
	result, err := c.search.Search(request.Request.Context(), knowledge.SearchRequest{Query: r.Query, Mode: r.Mode, TopK: r.TopK, Filters: r.Filters, DenseMinScore: r.DenseMinScore})
	middleware.Respond(request, (*v1.SearchRes)(&result), err)
}
func (c *WriteControllerV1) Create(request *gin.Context) {
	d, err := c.ingest(request, "", true)
	middleware.Respond(request, (*v1.CreateRes)(d), err)
}
func (c *WriteControllerV1) Update(request *gin.Context) {
	if !validDocumentID(request) {
		return
	}
	d, err := c.ingest(request, request.Param("id"), false)
	middleware.Respond(request, (*v1.UpdateRes)(d), err)
}
func (c *WriteControllerV1) ingest(request *gin.Context, id string, create bool) (*knowledge.DocumentInfo, error) {
	ctx := request.Request.Context()
	file, err := request.FormFile("file")
	if err != nil {
		return nil, knowledge.ErrInvalidDocument
	}
	patch := knowledge.MetadataPatch{}
	raw := request.PostForm("metadata")
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
	source := request.PostForm("source")
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
		version, present := request.GetPostForm("expectedVersion")
		if !present {
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

func validDocumentID(request *gin.Context) bool {
	if !documentID.MatchString(request.Param("id")) {
		middleware.Respond(request, nil, middleware.ErrInvalidRequest)
		return false
	}
	return true
}
