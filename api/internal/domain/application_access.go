package domain

import (
	"fmt"
	"slices"
	"strings"
)

func (key APIKey) SubjectID() string {
	if key.SubjectUserID != "" {
		return key.SubjectUserID
	}
	return key.CreatorID
}

func ApplicationScopes() []string {
	return []string{"read", "write", "create_issues", "create_comments", "app:mentionable", "app:assignable", "customer:read", "customer:write", "initiative:read", "initiative:write", "release:read", "release:create", "releaseStage:update"}
}

func NormalizeApplicationScopes(scopes []string) ([]string, error) {
	result := []string{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		switch scope {
		case "issues:create":
			scope = "create_issues"
		case "comments:create":
			scope = "create_comments"
		}
		if !slices.Contains(ApplicationScopes(), scope) {
			return nil, fmt.Errorf("unsupported application scope: %s", scope)
		}
		if !slices.Contains(result, scope) {
			result = append(result, scope)
		}
	}
	if !slices.Contains(result, "read") {
		return nil, fmt.Errorf("application read scope is required")
	}
	return result, nil
}
