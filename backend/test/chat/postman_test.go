package chat_test

import (
	"context"
	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestPostmanServerHelper(t *testing.T) {
	ready := os.Getenv("ONCALL_CHAT_HELPER_READY")
	if ready == "" {
		t.Skip("explicit local Postman server helper")
	}
	base := os.Getenv("ONCALL_CHAT_SIM_BASE_URL")
	if base == "" {
		t.Fatal("ONCALL_CHAT_SIM_BASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	model, err := client.NewOpenAI(ctx, config.ChatModel{BaseURL: base, Model: "fixture-model", APIKey: "fixture-only", Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	service, _ := graphService(t, model.ToolCallingModel(), 2*time.Second)
	url := startAPI(t, service)
	if err := os.WriteFile(ready, []byte(url), 0600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}
