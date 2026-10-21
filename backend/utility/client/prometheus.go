package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type Prometheus struct {
	http      *http.Client
	transport *http.Transport
	endpoint  string
	timeout   time.Duration
}

func NewPrometheus(c config.ToolHTTP) (*Prometheus, error) {
	if c.Validate() != nil {
		return nil, operations.Failure(operations.PrometheusTool, operations.ErrConfiguration)
	}
	h, transport := newToolHTTP(c)
	h.Transport = &observedTransport{h.Transport, "Prom", "alerts"}
	return &Prometheus{h, transport, strings.TrimRight(c.URL, "/") + "/api/v1/alerts", c.Timeout}, nil
}

func (p *Prometheus) Close() { p.transport.CloseIdleConnections() }

func (p *Prometheus) Alerts(ctx context.Context) (out operations.AlertsResult, err error) {
	started := time.Now()
	defer func() {
		fields := []logger.Field{logger.F("elapsed", time.Since(started))}
		if err != nil {
			var failure *operations.Error
			code := logger.Code(err)
			if errors.As(err, &failure) {
				code = failure.Code
			}
			logger.Warn(ctx, "Prom", "告警查询失败", append(fields, logger.F("code", code))...)
		} else {
			logger.Info(ctx, "Prom", "告警查询完成", append(fields, logger.F("count", len(out.Alerts)))...)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrConfiguration)
	}
	req.Header.Set("Accept", "application/json")
	response, err := p.http.Do(req)
	if err != nil {
		return operations.AlertsResult{}, operations.RequestFailure(ctx, operations.PrometheusTool, err)
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrAuthentication)
	}
	if response.StatusCode != 200 {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrUpstream)
	}
	if mediaType(response.Header.Get("Content-Type")) != "application/json" {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return operations.AlertsResult{}, operations.RequestFailure(ctx, operations.PrometheusTool, err)
		}
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
	}
	var result struct {
		Status string `json:"status"`
		Data   *struct {
			Alerts json.RawMessage `json:"alerts"`
		} `json:"data"`
	}
	if len(body) > maxToolResponseBytes || json.Unmarshal(body, &result) != nil {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
	}
	if result.Status == "error" {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrUpstream)
	}
	if result.Status != "success" || result.Data == nil || len(result.Data.Alerts) == 0 || result.Data.Alerts[0] != '[' {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
	}
	var alerts []operations.Alert
	if json.Unmarshal(result.Data.Alerts, &alerts) != nil {
		return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
	}
	for _, alert := range alerts {
		if alert.Labels == nil || alert.Annotations == nil || alert.Value == "" || (alert.State != "firing" && alert.State != "pending") {
			return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
		}
		if _, err := time.Parse(time.RFC3339Nano, alert.ActiveAt); err != nil {
			return operations.AlertsResult{}, operations.Failure(operations.PrometheusTool, operations.ErrInvalidResponse)
		}
	}
	if ctx.Err() != nil {
		return operations.AlertsResult{}, operations.RequestFailure(ctx, operations.PrometheusTool, ctx.Err())
	}
	return operations.AlertsResult{Source: p.endpoint, Alerts: alerts}, nil
}
