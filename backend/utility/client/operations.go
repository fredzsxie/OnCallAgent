package client

import (
	"context"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/operations"
)

type Operations struct {
	Prometheus *Prometheus
}

func OpenOperations(_ context.Context, enabled config.Tools) (*Operations, error) {
	clients := &Operations{}
	if enabled.QueryPrometheusAlertsEnabled {
		cfg, loadErr := config.LoadPrometheus()
		if loadErr != nil {
			return nil, operations.Failure(operations.PrometheusTool, operations.ErrConfiguration)
		}
		prometheus, err := NewPrometheus(cfg)
		if err != nil {
			return nil, err
		}
		clients.Prometheus = prometheus
	}
	return clients, nil
}

func (c *Operations) Close() error {
	if c.Prometheus != nil {
		c.Prometheus.Close()
	}
	return nil
}
