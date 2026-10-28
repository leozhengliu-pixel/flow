package main

import (
	"testing"

	"flow/api/internal/domain"
)

func TestChatPlatformDeploymentSetting(t *testing.T) {
	for value, want := range map[string]string{"": "slack", "slack": "slack", " Lark ": "lark", "FEISHU": "feishu", "teams": "slack"} {
		t.Setenv("FLOW_CHAT_PLATFORM", value)
		if got := chatPlatform(); got != want {
			t.Fatalf("FLOW_CHAT_PLATFORM=%q: %q, want %q", value, got, want)
		}
	}
	t.Setenv("FLOW_CHAT_PLATFORM", "lark")
	data := domain.Bootstrap{}
	sanitizeBootstrap(&data)
	if data.ChatPlatform != "lark" {
		t.Fatalf("bootstrap chat platform %q", data.ChatPlatform)
	}
}
