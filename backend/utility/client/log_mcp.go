package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type MCPToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type LogMCP struct {
	client      *mcpclient.Client
	timeout     time.Duration
	definitions []MCPToolDefinition
	closeOnce   sync.Once
	closeErr    error
}

type silentMCPLogger struct{}

func (silentMCPLogger) Infof(string, ...any)  {}
func (silentMCPLogger) Errorf(string, ...any) {}
func (silentMCPLogger) Debugf(string, ...any) {}

func NewLogMCP(ctx context.Context, cfg config.LogMCP) (*LogMCP, error) {
	if err := cfg.Validate(); err != nil {
		return nil, operations.Failure(operations.LogTool, operations.ErrConfiguration)
	}
	if ctx.Err() != nil {
		return nil, operations.RequestFailure(ctx, operations.LogTool, ctx.Err())
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	childEnv := stdioEnvironment(cfg)
	cli, err := mcpclient.NewStdioMCPClientWithOptions(cfg.Command, childEnv, cfg.Args,
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = env
			return cmd, nil
		}),
		transport.WithCommandLogger(silentMCPLogger{}),
	)
	if err != nil {
		return nil, operations.Failure(operations.LogTool, operations.ErrConfiguration)
	}
	c := &LogMCP{client: cli, timeout: cfg.Timeout}
	if stderr, ok := mcpclient.GetStderr(cli); ok {
		go func() { _, _ = io.Copy(io.Discard, stderr) }()
	}
	success := false
	defer func() {
		if !success {
			_ = c.Close()
		}
	}()
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "OnCallAgent", Version: "1.0.0"}
	if _, err := cli.Initialize(ctx, request); err != nil {
		return nil, mcpFailure(ctx, err)
	}
	listed, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, mcpFailure(ctx, err)
	}
	found := false
	for _, item := range listed.Tools {
		schema, err := json.Marshal(item.InputSchema)
		if err != nil {
			return nil, operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
		}
		c.definitions = append(c.definitions, MCPToolDefinition{item.Name, item.Description, schema})
		if item.Name == "SearchLog" {
			if found || !compatibleLogSchema(item.InputSchema) {
				return nil, logSchemaFailure()
			}
			found = true
		}
	}
	if !found {
		return nil, logSchemaFailure()
	}
	if ctx.Err() != nil {
		return nil, operations.RequestFailure(ctx, operations.LogTool, ctx.Err())
	}
	success = true
	return c, nil
}

func stdioEnvironment(cfg config.LogMCP) []string {
	values := map[string]string{
		"TRANSPORT":               "stdio",
		"TENCENTCLOUD_SECRET_ID":  cfg.SecretID,
		"TENCENTCLOUD_SECRET_KEY": cfg.SecretKey,
		"TZ":                      cfg.TimeZone,
	}
	for _, name := range []string{"PATH", "HOME"} {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	if cfg.APIBaseHost != "" {
		values["TENCENTCLOUD_API_BASE_HOST"] = cfg.APIBaseHost
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env
}

func logSchemaFailure() error {
	err := operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
	err.Message = "CLS SearchLog tool or input schema is incompatible"
	return err
}

func compatibleLogSchema(s mcp.ToolInputSchema) bool {
	if s.Type != "object" {
		return false
	}
	expected := map[string]string{"Region": "string", "TopicId": "string", "Query": "string", "From": "number", "To": "number", "Limit": "number"}
	for name, kind := range expected {
		property, ok := s.Properties[name].(map[string]any)
		if !ok {
			return false
		}
		typ, _ := property["type"].(string)
		if typ != kind && !(kind == "number" && typ == "integer") {
			return false
		}
	}
	for _, name := range s.Required {
		if _, ok := expected[name]; !ok {
			return false
		}
	}
	return true
}

func (c *LogMCP) Tools() []MCPToolDefinition {
	result := make([]MCPToolDefinition, len(c.definitions))
	for i, item := range c.definitions {
		result[i] = item
		result[i].InputSchema = append(json.RawMessage(nil), item.InputSchema...)
	}
	return result
}

func (c *LogMCP) Close() error {
	c.closeOnce.Do(func() {
		if err := c.client.Close(); err != nil {
			c.closeErr = operations.Failure(operations.LogTool, operations.ErrUpstream)
		}
	})
	return c.closeErr
}

func (c *LogMCP) Search(ctx context.Context, input operations.LogRequest) ([]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request := mcp.CallToolRequest{}
	request.Params.Name = "SearchLog"
	request.Params.Arguments = map[string]any{"Region": input.Region, "TopicId": input.TopicID, "Query": input.Query, "From": input.From, "To": input.To, "Limit": input.Limit}
	result, err := c.client.CallTool(ctx, request)
	if err != nil {
		return nil, mcpFailure(ctx, err)
	}
	if ctx.Err() != nil {
		return nil, operations.RequestFailure(ctx, operations.LogTool, ctx.Err())
	}
	if result == nil {
		return nil, operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
	}
	var payload []byte
	if result.StructuredContent != nil {
		payload, err = json.Marshal(result.StructuredContent)
	} else if len(result.Content) == 1 {
		if content, ok := result.Content[0].(mcp.TextContent); ok {
			payload = []byte(content.Text)
		}
	}
	if result.IsError {
		var failure struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(payload, &failure)
		if strings.HasPrefix(failure.Code, "AuthFailure") || strings.HasPrefix(failure.Code, "UnauthorizedOperation") {
			return nil, operations.Failure(operations.LogTool, operations.ErrAuthentication)
		}
		return nil, operations.Failure(operations.LogTool, operations.ErrUpstream)
	}
	if err != nil || len(payload) == 0 || len(payload) > maxToolResponseBytes {
		return nil, operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
	}
	var records []json.RawMessage
	if result.StructuredContent != nil && len(payload) > 0 && payload[0] == '{' {
		var structured struct {
			Records json.RawMessage `json:"records"`
		}
		if json.Unmarshal(payload, &structured) != nil {
			return nil, operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
		}
		payload = structured.Records
	}
	if json.Unmarshal(payload, &records) != nil || records == nil {
		return nil, operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
	}
	for _, record := range records {
		var object map[string]json.RawMessage
		if json.Unmarshal(record, &object) != nil || object == nil {
			return nil, operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
		}
	}
	return records, nil
}

func mcpFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return operations.RequestFailure(ctx, operations.LogTool, err)
	}
	var syntax *json.SyntaxError
	var shape *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &shape) {
		return operations.Failure(operations.LogTool, operations.ErrInvalidResponse)
	}
	return operations.Failure(operations.LogTool, operations.ErrUpstream)
}
