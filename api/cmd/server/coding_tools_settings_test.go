package main

import (
	"testing"

	"flow/api/internal/domain"
)

func TestValidateCodingToolSettings(t *testing.T) {
	valid := domain.UserSettings{EnabledCodingTools: []string{"cursor", "cursor", "customUrl"}, CustomDeepLinkURLTemplate: " https://tool.example/new?prompt={{prompt}} "}
	if err := validateCodingToolSettings(&valid); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	if len(valid.EnabledCodingTools) != 2 || valid.CustomDeepLinkURLTemplate != "https://tool.example/new?prompt={{prompt}}" {
		t.Fatalf("settings not normalized: %+v", valid)
	}
	for name, input := range map[string]domain.UserSettings{
		"unknown tool":    {EnabledCodingTools: []string{"notATool"}},
		"javascript link": {CustomDeepLinkURLTemplate: "javascript:alert(1)"},
		"custom scheme":   {CustomDeepLinkURLTemplate: "file:///etc/passwd"},
	} {
		if err := validateCodingToolSettings(&input); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
