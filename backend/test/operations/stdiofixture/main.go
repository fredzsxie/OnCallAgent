package main

import (
	"bufio"
	"encoding/json"
	"os"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Name            string         `json:"name"`
		Arguments       map[string]any `json:"arguments"`
	} `json:"params"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--exit-before-handshake" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	w := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var req request
		if json.Unmarshal(scanner.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			version := req.Params.ProtocolVersion
			if version == "" {
				version = "2025-06-18"
			}
			result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "stdio-fixture", "version": "1"}}
		case "tools/list":
			props := map[string]any{"Region": map[string]string{"type": "string"}, "TopicId": map[string]string{"type": "string"}, "Query": map[string]string{"type": "string"}, "From": map[string]string{"type": "number"}, "To": map[string]string{"type": "number"}, "Limit": map[string]string{"type": "number"}}
			if len(os.Args) > 1 && os.Args[1] == "--bad-schema" {
				delete(props, "TopicId")
			}
			result = map[string]any{"tools": []any{map[string]any{"name": "SearchLog", "description": "fixture", "inputSchema": map[string]any{"type": "object", "properties": props}}}}
		case "tools/call":
			if len(os.Args) > 1 && os.Args[1] == "--hang-call" {
				for scanner.Scan() {
				}
				return
			}
			if len(os.Args) > 1 && os.Args[1] == "--ops-log-fail" {
				result = map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": `{"code":"AuthFailure.SignatureFailure","message":"fixture-private-secret"}`}}}
				break
			}
			if len(os.Args) > 1 && os.Args[1] == "--ops-log" {
				result = map[string]any{"content": []any{map[string]string{"type": "text", "text": `[{"Time":1792382400000,"Source":"node-a","LogJson":{"level":"ERROR","message":"signature cache expired"}}]`}}}
				break
			}
			record, _ := json.Marshal([]any{map[string]any{
				"received":            req.Params.Arguments,
				"has_model_key":       os.Getenv("ONCALL_CHAT_MODEL_API_KEY") != "",
				"has_milvus_password": os.Getenv("ONCALL_MILVUS_PASSWORD") != "",
				"secret_id":           os.Getenv("TENCENTCLOUD_SECRET_ID"),
				"secret_key":          os.Getenv("TENCENTCLOUD_SECRET_KEY"),
				"transport":           os.Getenv("TRANSPORT"),
			}})
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": string(record)}}}
		default:
			continue
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": result})
		_, _ = w.Write(response)
		_ = w.WriteByte('\n')
		_ = w.Flush()
	}
}
