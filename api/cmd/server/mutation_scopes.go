package main

import (
	"context"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// withIssueScope runs a workspace mutation against just the named issue
// records (typically ids the callback validates) instead of loading every
// issue and content record in the workspace.
func withIssueScope(ctx context.Context, ids ...*string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{IssueIDs: scopeIDs(ids...)})
}

func scopeIDs(ids ...*string) []string {
	result := []string{}
	for _, id := range ids {
		if id == nil {
			continue
		}
		for _, value := range []string{*id, strings.TrimSpace(*id)} {
			if value != "" && !slices.Contains(result, value) {
				result = append(result, value)
			}
		}
	}
	return result
}

// releaseMutationScope loads the issues a release write can touch: ids it
// validates, and — when the write can move the release to a released status —
// the release's current issues, which completion automations update.
func releaseMutationScope(ctx context.Context, input releaseInput, match func(domain.Release) bool) context.Context {
	scope := store.MutationScope{}
	if input.IssueIDs != nil {
		scope.IssueIDs = normalizedStrings(*input.IssueIDs)
	}
	if input.Status != nil || input.Stage != nil {
		scope.Resolve = func(data domain.Bootstrap) store.MutationScope {
			extra := store.MutationScope{}
			for _, release := range data.Releases {
				if match(release) {
					extra.IssueIDs = append(extra.IssueIDs, release.IssueIDs...)
				}
			}
			return extra
		}
	}
	return store.WithMutationScope(ctx, scope)
}

// documentContentScope loads just a document's comment thread (comments are
// content records owned by the document id; the route may use its slug).
func documentContentScope(ctx context.Context, id string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		if document, err := documentByID(&data, id); err == nil {
			return store.MutationScope{Resources: []string{document.ID}}
		}
		return store.MutationScope{}
	}})
}
