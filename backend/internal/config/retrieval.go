package config

import (
	"fmt"
	"math"
	"strconv"
	"time"
	_ "time/tzdata"
)

const DefaultRetrievalTopK = 3

type Retrieval struct {
	TopK           int
	ScoreThreshold float64
}

func LoadRetrieval() (Retrieval, error) {
	c := Retrieval{}
	var err error
	if c.TopK, err = strconv.Atoi(env("ONCALL_RETRIEVAL_TOP_K", "3")); err != nil {
		return c, fmt.Errorf("ONCALL_RETRIEVAL_TOP_K must be an integer")
	}
	if c.ScoreThreshold, err = strconv.ParseFloat(env("ONCALL_RETRIEVAL_SCORE_THRESHOLD", "0.5"), 64); err != nil {
		return c, fmt.Errorf("ONCALL_RETRIEVAL_SCORE_THRESHOLD must be a number")
	}
	return c, c.Validate()
}

func (c Retrieval) Validate() error {
	if c.TopK < 1 || c.TopK > 16384 {
		return fmt.Errorf("retrieval top_k must be between 1 and 16384")
	}
	if math.IsNaN(c.ScoreThreshold) || math.IsInf(c.ScoreThreshold, 0) || c.ScoreThreshold < -1 || c.ScoreThreshold > 1 {
		return fmt.Errorf("retrieval score threshold must be finite and between -1 and 1")
	}
	return nil
}

type Tools struct {
	QueryInternalDocsEnabled     bool
	GetCurrentTimeEnabled        bool
	QueryPrometheusAlertsEnabled bool
	TimeZone                     string
}

func LoadTools() (Tools, error) {
	c := Tools{TimeZone: env("ONCALL_TIME_ZONE", "Asia/Shanghai")}
	var err error
	if c.QueryInternalDocsEnabled, err = strconv.ParseBool(env("ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED", "true")); err != nil {
		return c, fmt.Errorf("ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED must be a boolean")
	}
	if c.GetCurrentTimeEnabled, err = strconv.ParseBool(env("ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED", "true")); err != nil {
		return c, fmt.Errorf("ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED must be a boolean")
	}
	if c.QueryPrometheusAlertsEnabled, err = strconv.ParseBool(env("ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED", "false")); err != nil {
		return c, fmt.Errorf("ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED must be a boolean")
	}
	return c, c.Validate()
}

func (c Tools) Validate() error {
	if c.TimeZone == "" {
		return fmt.Errorf("ONCALL_TIME_ZONE is required")
	}
	if c.TimeZone == "Local" {
		return fmt.Errorf("ONCALL_TIME_ZONE must be an explicit IANA time zone")
	}
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return fmt.Errorf("ONCALL_TIME_ZONE must be a valid IANA time zone")
	}
	return nil
}
