package main

import (
	"encoding/json"
	"strings"
)

func redactIntegrationEvent(raw json.RawMessage) json.RawMessage {
	var value any
	if len(raw) == 0 {
		return raw
	}
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{}`)
	}
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				lower := strings.ToLower(key)
				if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || lower == "oauthstate" || lower == "authorization" || lower == "apikey" {
					delete(node, key)
				} else {
					visit(child)
				}
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(value)
	encoded, _ := json.Marshal(value)
	return encoded
}
