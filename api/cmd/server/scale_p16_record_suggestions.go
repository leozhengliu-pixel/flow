package main

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) pruneProjectRecordSuggestions(ctx context.Context, workspace, id string) {
	stale := func(item domain.IssueSuggestion) bool { return item.Type == "project" && item.SuggestedProjectID == id }
	if !projectRecordMutationsEnabled("project") {
		s.pruneIssueSuggestionTargets(ctx, workspace, stale)
		return
	}
	metadata, ok := s.store.WorkspaceMetadataFields(workspace, "issueSuggestions")
	if !ok {
		return
	}
	ids := []string{}
	for _, item := range metadata.IssueSuggestions {
		if stale(item) && !slices.Contains(ids, item.IssueID) {
			ids = append(ids, item.IssueID)
		}
	}
	for start := 0; start < len(ids); start += 500 {
		batch := ids[start:min(start+500, len(ids))]
		err := s.store.MutateWorkspace(store.WithIssueRecordMutations(ctx, batch...), workspace, "issue.suggestions_pruned", batch[0], nil, func(data *domain.Bootstrap) error {
			data.IssueSuggestions = slices.DeleteFunc(data.IssueSuggestions, stale)
			now := time.Now().UTC()
			for index := range data.Issues {
				if syncIssueSuggestionTargets(data, &data.Issues[index]) {
					data.Issues[index].Version++
					data.Issues[index].UpdatedAt = now
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, store.ErrNoMutation) {
			log.Printf("prune project suggestions workspace=%s: %v", workspace, err)
		}
	}
}
