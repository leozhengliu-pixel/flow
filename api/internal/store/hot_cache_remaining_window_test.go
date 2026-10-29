package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func holdCacheCommit(t *testing.T, pair [2]*SQLiteStore, write func(context.Context) error, check func()) {
	t.Helper()
	mark, reached, release := pauseCommits(t, pair[:], "sequenced")
	done := make(chan error, 1)
	go func() { done <- write(mark(t.Context())) }()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-reached:
	case err := <-done:
		done <- nil
		t.Fatalf("write returned before the barrier: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("write never reached commit")
	}
	check()
}

func cacheWindowWorkspace(t *testing.T, writer *SQLiteStore) (string, domain.Issue) {
	t.Helper()
	workspace := fmt.Sprintf("cache-window-%d", time.Now().UnixNano())
	data, err := writer.CreateWorkspace(t.Context(), "Cache window", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.DeleteWorkspace(context.Background(), workspace); err != nil {
			t.Error(err)
		}
	})
	return workspace, domain.Issue{Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, Title: "Cache window"}
}

func TestHotCacheRevisionForWritesWithoutIssueChanges(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			writer, reader := pair[0], pair[1]
			ctx := t.Context()
			workspace, base := cacheWindowWorkspace(t, writer)
			issue := createPeerIssue(t, writer, workspace, fmt.Sprintf("remaining-window-%d", time.Now().UnixNano()), base)
			query := IssueRecordQuery{Workspace: workspace, IssueIDs: []string{issue.ID}, Archived: "false", Limit: 10}
			releaseID := issue.ID + "-release"
			released := IssueRecordQuery{Workspace: workspace, Archived: "false", GroupBy: "none", Filter: IssueFilter{Field: "release", Values: []string{releaseID}}}
			for _, event := range []string{"comment.created", "relation.created", "release.updated"} {
				t.Run(event, func(t *testing.T) {
					before, err := reader.issueCountRevision(ctx, workspace)
					if err != nil {
						t.Fatal(err)
					}
					for range 2 {
						if _, err := reader.QueryIssueRecords(ctx, query); err != nil {
							t.Fatal(err)
						}
					}
					if _, _, hit := reader.cacheGetIssueQuery(ctx, query); !hit {
						t.Fatal("query was not cached before the write")
					}
					if event == "release.updated" {
						for range 2 {
							if groups, err := reader.QueryIssueGroups(ctx, released); err != nil || len(groups) != 0 {
								t.Fatalf("release before write: %v, %v", groups, err)
							}
						}
					}
					holdCacheCommit(t, pair, func(ctx context.Context) error {
						if event == "comment.created" {
							return writer.MutateWorkspace(WithIssueDiscussionMutation(ctx, issue.ID, "window-comment"), workspace, event, issue.ID, nil, func(data *domain.Bootstrap) error {
								data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: "window-comment", Body: "committed discussion", CreatedAt: time.Now().UTC()})
								return nil
							})
						}
						if event == "release.updated" {
							return writer.MutateWorkspace(ctx, workspace, event, releaseID, nil, func(data *domain.Bootstrap) error {
								data.Releases = append(data.Releases, domain.Release{ID: releaseID, IssueIDs: []string{issue.ID}})
								return nil
							})
						}
						// A no-op relationship event retires the issue caches
						// even when its callback changes no issue record.
						return writer.MutateWorkspace(ctx, workspace, event, issue.ID, nil, func(*domain.Bootstrap) error { return nil })
					}, func() {
						after, err := reader.issueCountRevision(ctx, workspace)
						if err != nil || after <= before {
							t.Fatalf("revision before %d after %d: %v", before, after, err)
						}
						if _, _, hit := reader.cacheGetIssueQuery(ctx, query); hit {
							t.Fatal("old query survived committed event")
						}
						if event == "release.updated" {
							groups, err := reader.QueryIssueGroups(ctx, released)
							if err != nil || len(groups) != 1 || groups[0].Count != 1 {
								t.Fatalf("release after commit: %v, %v", groups, err)
							}
						}
						stored, err := reader.IssueRecord(ctx, workspace, issue.ID)
						if err != nil || stored.Version != issue.Version {
							t.Fatalf("event wrote issue record: %d, %v", stored.Version, err)
						}
					})
				})
			}
		})
	}
}

func TestHotCacheMetadataRevisionForUsageAndMigration(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			writer, reader := pair[0], pair[1]
			ctx := t.Context()
			workspace, _ := cacheWindowWorkspace(t, writer)
			keyID := fmt.Sprintf("window-key-%d", time.Now().UnixNano())
			projectID := keyID + "-project"
			if err := writer.MutateWorkspace(ctx, workspace, "test.window", keyID, nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: keyID, CreatorID: data.Viewer.ID})
				data.OAuthAuthorizations = append(data.OAuthAuthorizations, domain.OAuthAuthorization{ID: keyID})
				data.Projects = append(data.Projects, domain.Project{ID: projectID, Name: "Cache window", SlugID: "cache-window"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			read := func() domain.Bootstrap {
				readCtx, release, err := reader.BeginWorkspaceRead(ctx, workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				return readCtx.Value(workspaceReadKey{}).(workspaceRead).metadata
			}
			for range 2 {
				read()
			}
			at := time.Now().UTC()
			holdCacheCommit(t, pair, func(ctx context.Context) error {
				return writer.writeAPIKeyUse(ctx, apiKeyUse{workspace: workspace, keyID: keyID, authorizationID: keyID, at: at})
			}, func() {
				data := read()
				found := 0
				for _, key := range data.APIKeys {
					if key.ID == keyID && key.LastUsedAt != nil && key.LastUsedAt.Equal(at) {
						found++
					}
				}
				for _, grant := range data.OAuthAuthorizations {
					if grant.ID == keyID && grant.LastUsedAt != nil && grant.LastUsedAt.Equal(at) {
						found++
					}
				}
				if found != 2 {
					t.Fatalf("usage timestamp stale in %d credential records", 2-found)
				}
			})
			projectSlug := func() string {
				for _, project := range read().Projects {
					if project.ID == projectID {
						return project.SlugID
					}
				}
				return "(missing)"
			}
			for range 2 {
				if got := projectSlug(); got != "cache-window" {
					t.Fatalf("project before migration: %s", got)
				}
			}
			holdCacheCommit(t, pair, func(ctx context.Context) error {
				_, _, err := writer.migrateWorkspaceResourceSlugs(ctx, workspace)
				return err
			}, func() {
				if got := projectSlug(); got == "cache-window" || got == "(missing)" {
					t.Fatalf("project after migration: %s", got)
				}
			})
		})
	}
}
