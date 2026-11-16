package plan_execute_replan

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/fredzsxie/OnCallAgent/backend/internal/ai/retriever"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

func clip(value string, limit int) string {
	r := []rune(value)
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return value
}
func toolTitle(name string) string {
	switch name {
	case "get_current_time":
		return "核对当前时间"
	case "query_prometheus_alerts":
		return "查询 Prometheus 告警"
	case "query_internal_docs":
		return "检索内部排查手册"
	}
	return "调用工具：" + clip(name, 128)
}
func toolInput(name, args string) *StepInput {
	if name != "query_internal_docs" {
		return nil
	}
	var in struct {
		Query string `json:"query"`
	}
	if json.Unmarshal([]byte(args), &in) != nil {
		return nil
	}
	return &StepInput{Query: clip(in.Query, 1024)}
}
func safeSource(value string) string {
	if u, err := url.Parse(value); err == nil && u.IsAbs() {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		value = u.String()
	}
	return clip(value, 1024)
}
func safeFields(fields map[string]string) map[string]string {
	out := map[string]string{}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys[:min(len(keys), 50)] {
		v := fields[k]
		lower := strings.ToLower(k)
		for _, secret := range []string{"password", "secret", "token", "authorization", "credential", "api_key", "apikey"} {
			if strings.Contains(lower, secret) {
				v = "[已隐藏]"
				break
			}
		}
		out[clip(k, 128)] = clip(v, 1024)
	}
	return out
}
func publicToolError(f *operations.Error) *StepError {
	messages := map[string]string{"upstream_error": "上游服务请求失败，未获得有效结果", "authentication_failed": "认证失败，请检查工具访问配置", "invalid_response": "工具返回的数据格式无效", "invalid_input": "工具调用参数无效", "invalid_configuration": "工具配置无效", "timeout": "工具请求超时", "cancelled": "工具调用已取消"}
	message := messages[f.Code]
	if message == "" {
		message = "工具执行失败"
	}
	return &StepError{f.Code, message, f.Retryable}
}

// Parse only known public result fields. Raw tool/provider payloads stay inside the agent.
func toolResult(name, raw string) (*StepOutput, *int, string, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &envelope) != nil || envelope == nil {
		return nil, nil, "", operations.ErrInvalidResponse
	}
	out := &StepOutput{}
	switch name {
	case "get_current_time":
		var v struct {
			Timestamp   string `json:"timestamp"`
			TimeZone    string `json:"timezone"`
			UnixSeconds *int64 `json:"unix_seconds"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || v.Timestamp == "" || v.TimeZone == "" || v.UnixSeconds == nil {
			return nil, nil, "", operations.ErrInvalidResponse
		}
		out.Timestamp, out.TimeZone, out.UnixSeconds = clip(v.Timestamp, 128), clip(v.TimeZone, 128), v.UnixSeconds
		return out, nil, "当前时间：" + out.Timestamp + "（" + out.TimeZone + "）", nil
	case "query_prometheus_alerts":
		var v operations.AlertsResult
		if json.Unmarshal([]byte(raw), &v) != nil || v.Alerts == nil {
			return nil, nil, "", operations.ErrInvalidResponse
		}
		n := len(v.Alerts)
		out.Source = safeSource(v.Source)
		out.Truncated = n > 20
		for _, a := range v.Alerts[:min(n, 20)] {
			out.Alerts = append(out.Alerts, AlertDetail{safeFields(a.Labels), safeFields(a.Annotations), clip(a.State, 128), clip(a.ActiveAt, 128), clip(a.Value, 128)})
		}
		return out, &n, fmt.Sprintf("告警查询完成，共 %d 个活动告警实例", n), nil
	case "query_internal_docs":
		var v retriever.Result
		if json.Unmarshal([]byte(raw), &v) != nil || v.Documents == nil {
			return nil, nil, "", operations.ErrInvalidResponse
		}
		n := len(v.Documents)
		out.Query, out.Mode, out.ScoreType = clip(v.Query, 1024), clip(v.Mode, 128), clip(v.ScoreType, 128)
		out.Truncated = n > 20
		for _, d := range v.Documents[:min(n, 20)] {
			out.Documents = append(out.Documents, DocumentDetail{Title: clip(d.Title, 1024), Source: safeSource(d.Source), SectionPath: clip(d.SectionPath, 1024), Version: clip(d.Version, 128), Score: d.Score, ScoreType: clip(d.ScoreType, 128), Excerpt: clip(d.Content, 500), Truncated: len([]rune(d.Content)) > 500})
		}
		return out, &n, fmt.Sprintf("内部手册检索完成，命中 %d 个文档片段", n), nil
	default:
		return nil, nil, "工具调用完成", nil
	}
}
