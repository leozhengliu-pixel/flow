package main

import (
	"log"
	"os"
	"strings"
	"sync"
)

// FLOW_CHAT_PLATFORM chooses the deployment's first-class chat platform:
// slack (the default), lark or feishu. The web app routes its chat entry
// points (team channel, project and initiative updates, notifications,
// onboarding, the integration catalog) to that platform. It does not switch
// any integration off: the other chat integrations stay available in
// Settings > Integrations.
func chatPlatform() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_CHAT_PLATFORM")))
	switch value {
	case "", "slack":
		return "slack"
	case "lark", "feishu":
		return value
	}
	invalidChatPlatformOnce.Do(func() {
		log.Printf("FLOW_CHAT_PLATFORM=%q is not slack, lark or feishu; using slack", value)
	})
	return "slack"
}

var invalidChatPlatformOnce sync.Once
