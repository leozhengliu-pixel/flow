package main

import (
	"slices"

	"flow/api/internal/domain"
)

func filterApplicationScopeProjection(data *domain.Bootstrap, key domain.APIKey) {
	if key.Actor != "app" {
		return
	}
	if !apiKeyHasScope(key, "customer:read") && !apiKeyHasScope(key, "customer:write") {
		data.Customers = nil
		data.CustomerRequests = nil
		for i := range data.Projects {
			data.Projects[i].Customers = slices.DeleteFunc(data.Projects[i].Customers, func(string) bool { return true })
		}
	}
	if !apiKeyHasScope(key, "initiative:read") && !apiKeyHasScope(key, "initiative:write") {
		data.Initiatives = nil
		data.InitiativeRelations = nil
		data.InitiativeUpdates = nil
		for i := range data.Projects {
			data.Projects[i].Initiatives = slices.DeleteFunc(data.Projects[i].Initiatives, func(string) bool { return true })
		}
	}
	if !apiKeyHasScope(key, "release:read") {
		data.Releases = nil
		data.ReleasePipelines = nil
	}
}
