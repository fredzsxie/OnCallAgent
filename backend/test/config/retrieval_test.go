package config_test

import (
	"math"
	"testing"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

func TestRetrievalAndToolsDefaults(t *testing.T) {
	for _, name := range []string{"ONCALL_RETRIEVAL_TOP_K", "ONCALL_RETRIEVAL_SCORE_THRESHOLD", "ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED", "ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED", "ONCALL_TIME_ZONE"} {
		unsetEnv(t, name)
	}
	r, err := config.LoadRetrieval()
	if err != nil || r.TopK != 3 || r.ScoreThreshold != 0.5 {
		t.Fatalf("retrieval defaults: %+v %v", r, err)
	}
	tools, err := config.LoadTools()
	if err != nil || !tools.QueryInternalDocsEnabled || !tools.GetCurrentTimeEnabled || tools.TimeZone != "Asia/Shanghai" {
		t.Fatalf("tool defaults: %+v %v", tools, err)
	}
	t.Setenv("ONCALL_RETRIEVAL_TOP_K", "7")
	t.Setenv("ONCALL_RETRIEVAL_SCORE_THRESHOLD", "-0.1")
	t.Setenv("ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED", "false")
	t.Setenv("ONCALL_TIME_ZONE", "UTC")
	r, err = config.LoadRetrieval()
	if err != nil || r.TopK != 7 || r.ScoreThreshold != -0.1 {
		t.Fatal("retrieval overrides ignored")
	}
	tools, err = config.LoadTools()
	if err != nil || tools.QueryInternalDocsEnabled || tools.TimeZone != "UTC" {
		t.Fatal("tool overrides ignored")
	}
}

func TestInvalidRetrievalAndToolsConfig(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		tools       bool
	}{
		{"ONCALL_RETRIEVAL_TOP_K", "", false}, {"ONCALL_RETRIEVAL_TOP_K", "0", false}, {"ONCALL_RETRIEVAL_TOP_K", "16385", false}, {"ONCALL_RETRIEVAL_TOP_K", "x", false},
		{"ONCALL_RETRIEVAL_SCORE_THRESHOLD", "", false}, {"ONCALL_RETRIEVAL_SCORE_THRESHOLD", "x", false}, {"ONCALL_RETRIEVAL_SCORE_THRESHOLD", "NaN", false}, {"ONCALL_RETRIEVAL_SCORE_THRESHOLD", "+Inf", false}, {"ONCALL_RETRIEVAL_SCORE_THRESHOLD", "-1.01", false}, {"ONCALL_RETRIEVAL_SCORE_THRESHOLD", "1.01", false},
		{"ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED", "", true}, {"ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED", "", true}, {"ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED", "yes", true}, {"ONCALL_TIME_ZONE", "", true}, {"ONCALL_TIME_ZONE", "NoSuch/Zone", true}, {"ONCALL_TIME_ZONE", "Local", true},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			var err error
			if tc.tools {
				_, err = config.LoadTools()
			} else {
				_, err = config.LoadRetrieval()
			}
			if err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	for _, threshold := range []float64{-1, 1} {
		if err := (config.Retrieval{TopK: 16384, ScoreThreshold: threshold}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (config.Retrieval{TopK: 3, ScoreThreshold: math.Inf(-1)}).Validate(); err == nil {
		t.Fatal("infinite threshold accepted")
	}
}

func TestRetrievalEnvPriority(t *testing.T) {
	changeDir(t, t.TempDir())
	unsetEnv(t, "ONCALL_RETRIEVAL_TOP_K")
	unsetEnv(t, "ONCALL_RETRIEVAL_SCORE_THRESHOLD")
	writeEnv(t, ".env", "ONCALL_RETRIEVAL_TOP_K=7\nONCALL_RETRIEVAL_SCORE_THRESHOLD=0.75\n")
	t.Setenv("ONCALL_RETRIEVAL_TOP_K", "4")
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadRetrieval()
	if err != nil || cfg.TopK != 4 || cfg.ScoreThreshold != 0.75 {
		t.Fatalf("env priority: %+v %v", cfg, err)
	}
}
