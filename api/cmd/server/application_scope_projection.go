package main

import "flow/api/internal/domain"

func filterApplicationScopeProjection(data *domain.Bootstrap, key domain.APIKey) {
	if key.Actor != "app" {
		return
	}
	if !apiKeyHasScope(key, "customer:read") && !apiKeyHasScope(key, "customer:write") {
		data.Customers = nil
		data.CustomerRequests = nil
	}
	if !apiKeyHasScope(key, "initiative:read") && !apiKeyHasScope(key, "initiative:write") {
		data.Initiatives = nil
		data.InitiativeRelations = nil
		data.InitiativeUpdates = nil
	}
	if !apiKeyHasScope(key, "release:read") {
		data.Releases = nil
		data.ReleasePipelines = nil
	}
}
