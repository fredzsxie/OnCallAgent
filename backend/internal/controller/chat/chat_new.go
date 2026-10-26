package chat

import (
	chatservice "github.com/fredzsxie/OnCallAgent/backend/internal/service/chat"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/sse"
)

type ControllerV1 struct {
	service *chatservice.ChatService
	streams *sse.Service
}

func NewV1(service *chatservice.ChatService, streams *sse.Service) *ControllerV1 {
	return &ControllerV1{service: service, streams: streams}
}
