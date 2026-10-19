package client

import (
	"context"
	"errors"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

type Operations struct {
	Prometheus *Prometheus
	MCP        *LogMCP
	Logs       *operations.LogService
}

func OpenOperations(ctx context.Context, enabled config.Tools) (*Operations, error) {
	clients := &Operations{}
	var err error
	success := false
	defer func() {
		if !success {
			_ = clients.Close()
		}
	}()
	if enabled.QueryPrometheusAlertsEnabled {
		cfg, loadErr := config.LoadPrometheus()
		if loadErr != nil {
			return nil, operations.Failure(operations.PrometheusTool, operations.ErrConfiguration)
		}
		if clients.Prometheus, err = NewPrometheus(cfg); err != nil {
			return nil, err
		}
	}
	if enabled.QueryLogEnabled {
		cfg, loadErr := config.LoadLogMCP()
		if loadErr != nil {
			return nil, operations.Failure(operations.LogTool, operations.ErrConfiguration)
		}
		if clients.MCP, err = NewLogMCP(ctx, cfg); err != nil {
			return nil, err
		}
		if clients.Logs, err = operations.NewLogService(clients.MCP, cfg, nil); err != nil {
			return nil, err
		}
	}
	success = true
	return clients, nil
}

func (c *Operations) Close() error {
	var err error
	if c.MCP != nil {
		err = errors.Join(err, c.MCP.Close())
	}
	if c.Prometheus != nil {
		c.Prometheus.Close()
	}
	return err
}
