package main

import (
	"context"
	"log"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) normalizeLegacyRecurringIssuesBounded(ctx context.Context, key string, now time.Time) error {
	read, err := s.store.BeginRecurringNormalizationRead(ctx, key)
	if err != nil {
		return err
	}
	defer read.Close()
	locations := map[string]*time.Location{}
	for start := 0; start < len(read.IDs); start += 200 {
		err := func() error {
			page, err := read.Page(ctx, read.IDs[start:min(start+200, len(read.IDs))])
			if err != nil {
				return err
			}
			defer page.Close()
			for _, candidate := range page.Data.Issues {
				loc, found := locations[candidate.Team.ID]
				if !found {
					timezone, err := read.TeamTimezone(candidate.Team.ID)
					if err != nil {
						return err
					}
					loc = teamLocation(timezone)
					locations[candidate.Team.ID] = loc
				}
				if !normalizeRecurrenceTiming(&candidate, loc) {
					continue
				}
				if err := s.normalizeRecurringIssueTimingBounded(ctx, key, candidate.ID, now); err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *server) normalizeRecurringIssueTimingBounded(ctx context.Context, key, id string, now time.Time) error {
	ctx, closeOutputs := beginIndependentMetadataOutputReads(ctx)
	defer closeOutputs()
	ctx = store.ContextWithIssueRecurrenceTimingMutation(withIssueScope(ctx, &id))
	var updated domain.Issue
	err := s.store.MutateWorkspace(ctx, key, "issue.updated", id, map[string]string{"automation": "recurring"}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return store.ErrNoMutation
		}
		if !normalizeRecurrenceTiming(issue, teamLocation(teamSettings(data, issue.Team.ID).Timezone)) {
			return store.ErrNoMutation
		}
		issue.UpdatedAt = now
		issue.Version++
		updated = *issue
		return nil
	})
	if err != nil {
		return err
	}
	if updated.ID != "" {
		next := "none"
		if updated.NextOccurrenceAt != nil {
			next = updated.NextOccurrenceAt.Format(time.RFC3339)
		}
		log.Printf("Recurring issue workspace=%s issue=%s upgraded: due %s next %s", key, id, optionalID(updated.DueDate), next)
	}
	return nil
}
