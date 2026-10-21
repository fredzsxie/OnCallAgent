package chat

import (
	"bytes"
	"context"
	"encoding/json"
	v1 "github.com/fredzsxie/OnCallAgent/backend/api/chat/v1"
	pipeline "github.com/fredzsxie/OnCallAgent/backend/internal/ai/agent/plan_execute_replan"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/aiops"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
	"github.com/gogf/gf/v2/net/ghttp"
	"mime"
)

type AIOpsController struct{ service *aiops.AIOpsService }

func NewAIOpsV1(service *aiops.AIOpsService) *AIOpsController { return &AIOpsController{service} }

func (c *AIOpsController) AIOps(ctx context.Context, _ *v1.AIOpsReq) (*v1.AIOpsRes, error) {
	// GoFrame skips body decoding for a fieldless request; validate the fixed-task JSON envelope here.
	r := ghttp.RequestFromCtx(ctx)
	if r != nil && len(bytes.TrimSpace(r.GetBody())) > 0 {
		kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		var body map[string]json.RawMessage
		if kind != "application/json" || json.Unmarshal(r.GetBody(), &body) != nil || body == nil || len(body) != 0 {
			return nil, aiops.Wrap(pipeline.ErrInvalidInput, nil)
		}
	}
	logger.Info(ctx, "Ops", "用户请求", logger.F("request", "{}"))
	if c.service == nil {
		return nil, aiops.Wrap(aiops.ErrInitialization, nil)
	}
	out, err := c.service.Analyze(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.AIOpsRes{Result: out.Report, Detail: out.Detail}, nil
}
