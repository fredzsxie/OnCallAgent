package chat

import (
	"bytes"
	"encoding/json"
	"mime"

	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/fredzsxie/OnCallAgent/backend/utility/middleware"
	"github.com/gin-gonic/gin"
)

type AIOpsController struct{ service *aiops.AIOpsService }

func NewAIOpsV1(service *aiops.AIOpsService) *AIOpsController { return &AIOpsController{service} }

func (c *AIOpsController) AIOps(request *gin.Context) {
	if err := c.validate(request); err != nil {
		middleware.Respond(request, nil, err)
		return
	}
	out, err := c.service.Analyze(request.Request.Context())
	if err != nil {
		middleware.Respond(request, nil, err)
		return
	}
	middleware.Respond(request, &v1.AIOpsRes{Result: out.Report, Detail: out.Detail}, nil)
}

func (c *AIOpsController) AIOpsStream(request *gin.Context) {
	if err := c.validate(request); err != nil {
		middleware.Respond(request, nil, err)
		return
	}
	err := sse.ServeOps(request.Request.Context(), request.Writer, c.service)
	middleware.Respond(request, nil, err)
}

func (c *AIOpsController) validate(request *gin.Context) error {
	// Empty bodies remain valid; nonempty bodies must be exactly the fixed-task JSON object.
	raw, err := request.GetRawData()
	if err != nil {
		return aiops.Wrap(pipeline.ErrInvalidInput, nil)
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		kind, _, _ := mime.ParseMediaType(request.GetHeader("Content-Type"))
		var body map[string]json.RawMessage
		if kind != "application/json" || json.Unmarshal(raw, &body) != nil || body == nil || len(body) != 0 {
			return aiops.Wrap(pipeline.ErrInvalidInput, nil)
		}
	}
	logger.Info(request.Request.Context(), "Ops", "用户请求", logger.F("request", "{}"))
	if c.service == nil {
		return aiops.Wrap(aiops.ErrInitialization, nil)
	}
	return nil
}
