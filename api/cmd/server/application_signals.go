package main

import (
	"encoding/json"
	"fmt"
)

func validateApplicationSignal(kind, signal string, metadata map[string]any, app bool) error {
	if signal == "" {
		if len(metadata) != 0 {
			return fmt.Errorf("signalMetadata requires a signal")
		}
		return nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil || len(raw) > 16<<10 {
		return fmt.Errorf("signal metadata is too large")
	}
	if signal == "stop" && !app && kind == "prompt" {
		return nil
	}
	if !app || kind != "elicitation" {
		return fmt.Errorf("this signal requires an application elicitation")
	}
	switch signal {
	case "select":
		options, ok := metadata["options"].([]any)
		if !ok || len(options) == 0 || len(options) > 20 {
			return fmt.Errorf("select requires 1 to 20 options")
		}
		seen := map[string]bool{}
		for _, raw := range options {
			option, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid select option")
			}
			value, ok := option["value"].(string)
			if !ok || value == "" || len(value) > 2048 {
				return fmt.Errorf("select options require a non-empty value")
			}
			if seen[value] {
				return fmt.Errorf("select option values must be unique")
			}
			seen[value] = true
			if label, exists := option["label"]; exists {
				if text, ok := label.(string); !ok || len(text) > 512 {
					return fmt.Errorf("invalid select label")
				}
			}
		}
	case "auth":
		url, _ := metadata["url"].(string)
		if !validOAuthMetadataURI(url) {
			return fmt.Errorf("auth requires an HTTPS URL")
		}
		for _, field := range []string{"userId", "providerName"} {
			if value, exists := metadata[field]; exists {
				if text, ok := value.(string); !ok || len(text) > 200 {
					return fmt.Errorf("invalid auth metadata")
				}
			}
		}
	default:
		return fmt.Errorf("unsupported activity signal")
	}
	return nil
}
