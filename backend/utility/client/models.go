package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *OpenAI) ModelID() string { return c.cfg.Model }

// Models verifies provider metadata rather than trusting model self-descriptions.
func (c *OpenAI) Models(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, errors.New("could not construct GET /models request")
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("GET /models failed; check connectivity and API base URL")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /models returned HTTP %d; model catalog could not be verified", response.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
		return nil, errors.New("GET /models returned invalid model catalog JSON")
	}
	ids := make([]string, 0, len(body.Data))
	for _, model := range body.Data {
		if model.ID != "" {
			ids = append(ids, model.ID)
		}
	}
	return ids, nil
}
