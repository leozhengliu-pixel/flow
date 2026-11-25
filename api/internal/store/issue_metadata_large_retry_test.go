package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// This is a required-success legal-large variant of SW239. It reuses that
// test's callback capture, exact generated-value normalization and stack
// attribution helpers; no conflict or refusal is manufactured by the callback.
func TestScaleP21SW258IssueMetadataLargeConflictRetryDifferential(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "false")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, pair := range peerStoreSets(t) {
		t.Run(driver, func(t *testing.T) {
			peer, owner := pair[0], pair[1]
			user, token, err := peer.Register(t.Context(), "SW258 owner", idgen.New("sw258-")+"@example.test", "sw258-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			if err := peer.VerifyEmail(t.Context(), token); err != nil {
				t.Fatal(err)
			}
			data, err := peer.CreateWorkspace(ContextWithActor(t.Context(), user), "SW258 owned", idgen.New("sw258-workspace-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			workspace := data.Workspace.URLKey
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := peer.DeleteWorkspace(ctx, workspace); err != nil {
					t.Error(err)
				}
				if err := peer.EraseAccount(ctx, user.ID); err != nil {
					t.Error(err)
				}
			})
			at := time.Date(2026, 11, 16, 12, 13, 14, 123456789, time.UTC)
			destination := domain.Team{ID: "sw258-destination", Key: "SW258D", Name: "Destination"}
			peerTeam := domain.Team{ID: "sw258-peer-team", Key: "SW258P", Name: "Peer attachment"}
			destinationState := domain.WorkflowState{ID: "sw258-destination-state", TeamID: destination.ID, Name: "Started", Type: "started"}
			peerState := domain.WorkflowState{ID: "sw258-peer-state", TeamID: peerTeam.ID, Name: "Started", Type: "started"}
			issue := domain.Issue{
				ID: "sw258-moving-issue", Identifier: "SW258-1", Number: 1, Version: 1,
				Title: "Before", Team: data.Teams[0], State: data.States[0], Creator: user,
				CreatedAt: at, UpdatedAt: at,
			}
			other := issue
			other.ID, other.Identifier, other.Number = "sw258-other-issue", "SW258P-2", 2
			other.Team, other.State = peerTeam, peerState
			document := domain.Document{
				ID: "sw258-document", SlugID: "sw258-document", Title: "Attached",
				Content: strings.Repeat("L", 512<<10),
				Creator: user, IssueID: issue.ID, IssueTeamID: issue.Team.ID,
				InheritedTeamIDs: []string{issue.Team.ID}, TeamIDs: []string{}, ProjectIDs: []string{},
				CreatedAt: at, UpdatedAt: at,
			}
			legalRequest, err := json.Marshal(map[string]any{"content": document.Content, "issueId": issue.ID})
			if err != nil || len(document.Content) != 512<<10 || len(legalRequest) >= 1<<20 {
				t.Fatal("large document stopped fitting the supported plain-content request", len(legalRequest), err)
			}
			if metadataWorkBytes != 256<<20 {
				t.Fatal("legal-large retry witness changed the production work quota")
			}

			var wantOutput any
			var wantCallbacks []scaleP21SW239Callback
			var wantNative map[string]any
			var wantMetadata scaleP21SW76Metadata
			for _, bounded := range []bool{false, true} {
				t.Run(fmt.Sprintf("bounded=%t", bounded), func(t *testing.T) {
					if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := peer.MutateWorkspace(t.Context(), workspace, "scale_p21.sw258_seed", "", nil, func(current *domain.Bootstrap) error {
						for _, team := range []domain.Team{destination, peerTeam} {
							if !slices.ContainsFunc(current.Teams, func(item domain.Team) bool { return item.ID == team.ID }) {
								current.Teams = append(current.Teams, team)
							}
						}
						for _, state := range []domain.WorkflowState{destinationState, peerState} {
							if !slices.ContainsFunc(current.States, func(item domain.WorkflowState) bool { return item.ID == state.ID }) {
								current.States = append(current.States, state)
							}
						}
						current.Issues = slices.DeleteFunc(current.Issues, func(item domain.Issue) bool { return item.ID == issue.ID || item.ID == other.ID })
						current.Issues = append(current.Issues, issue, other)
						current.Documents = []domain.Document{document}
						current.Projects = []domain.Project{{ID: "sw258-unchanged-project", Name: "Unchanged", Description: strings.Repeat("unrelated body ", 1000), CreatedAt: at, UpdatedAt: at}}
						current.Activities[issue.ID], current.Comments[issue.ID] = []domain.ActivityEvent{}, []domain.Comment{}
						current.Notifications, current.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
						current.Webhooks = []domain.Webhook{{ID: "sw258-hook", URL: "https://example.invalid/sw258", Enabled: true}}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := owner.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					initial, err := owner.IssueRecord(t.Context(), workspace, issue.ID)
					if err != nil {
						t.Fatal(err)
					}
					if initial.Version != issue.Version || initial.Team.ID != issue.Team.ID {
						t.Fatal("literal fixture reset changed the measured issue preimage", initial.Version, initial.Team.ID)
					}
					source := owner
					if bounded {
						source, err = owner.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						defer source.releaseMetadataLogicalSources()
						if source.metadataResidency.work.limit != metadataWorkBytes {
							t.Fatal("large retry witness is not using the default work allowance")
						}
					}
					// Reuse the existing real-conflict fixture's stale-copy
					// interleave. The supported peer producer reattaches this
					// document while the measured source retains its old owner.
					metadataEdit(t, peer, workspace, "document.permissions_updated", document.ID, func(current *domain.Bootstrap) error {
						for index := range current.Documents {
							if current.Documents[index].ID == document.ID {
								current.Documents[index].IssueID = other.ID
								current.Documents[index].IssueTeamID = peerTeam.ID
								current.Documents[index].InheritedTeamIDs = []string{peerTeam.ID}
								current.Documents[index].UpdatedAt = at.Add(time.Hour)
							}
						}
						return nil
					})
					canonicalBefore := scaleP21SW76MetadataImage(t, peer.db, workspace)
					var rowBytes int
					var largeRaw []byte
					if err := peer.db.QueryRowContext(t.Context(),
						"SELECT LENGTH(data),data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' AND record_key=?",
						workspace, document.ID).Scan(&rowBytes, &largeRaw); err != nil {
						t.Fatal(err)
					}
					var canonicalDocument domain.Document
					if err := json.Unmarshal(largeRaw, &canonicalDocument); err != nil || canonicalDocument.Content != document.Content ||
						rowBytes != len(largeRaw) || rowBytes <= MetadataRecordPageMaxBytes || rowBytes >= 1<<20 {
						t.Fatal("fixture did not retain a legal document row above the conflict-refresh page cap", rowBytes, len(largeRaw), err)
					}
					t.Logf("bounded=%t canonical_document_bytes=%d page_cap=%d request_bytes=%d", bounded, rowBytes, MetadataRecordPageMaxBytes, len(legalRequest))

					// Require the real rebase algorithm's exact conflict key
					// without committing anything or manufacturing an error.
					stale, found := owner.storedWorkspace(workspace)
					if !found {
						t.Fatal("stale owner missing")
					}
					before, after := cloneCallbackInput(stale), cloneCallbackInput(stale)
					for index := range after.Documents {
						if after.Documents[index].ID == document.ID {
							after.Documents[index].IssueTeamID = destination.ID
							after.Documents[index].InheritedTeamIDs = []string{destination.ID}
						}
					}
					moved := initial
					moved.Team, moved.State = destination, destinationState
					probe, err := owner.db.BeginTx(t.Context(), owner.recordTxOptions())
					if err != nil {
						t.Fatal(err)
					}
					conflict := owner.rebaseIssueMetadata(t.Context(), probe, workspace, &before, &after, []domain.Issue{initial}, []domain.Issue{moved})
					keys, typed := conflictedMetadataKeys(conflict)
					if err := probe.Rollback(); err != nil {
						t.Fatal(err)
					}
					if !errors.Is(conflict, ErrConcurrentWrite) || !typed || !reflect.DeepEqual(keys, []metadataRecordKey{{"documents", document.ID}}) {
						t.Fatal("fixture did not create the real keyed document conflict", keys, conflict)
					}
					var callbacks []scaleP21SW239Callback
					var literalRefresh, logicalRefresh atomic.Int32
					ctx := WithSQLStatementObserver(t.Context(), func(query string) {
						if strings.HasPrefix(query, "SELECT record_key") {
							switch scaleP21SW239RefreshFrame() {
							case "literal":
								literalRefresh.Add(1)
							case "logical":
								logicalRefresh.Add(1)
							}
						}
					})
					var publicationMu sync.Mutex
					var webhooks []domain.DomainEvent
					var realtime []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						publicationMu.Lock()
						defer publicationMu.Unlock()
						webhooks = append(webhooks, event)
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						publicationMu.Lock()
						defer publicationMu.Unlock()
						realtime = append(realtime, event)
					})
					defer source.SetWebhookSink(nil)
					defer source.SetRealtimeSink(nil)
					priorEvents := scaleP21SW63EventIDs(t, source, workspace)
					started := time.Now().UTC()
					result, err := source.UpdateIssueRecord(ctx, workspace, issue.ID, &initial.Version,
						IssueMutationScope{Payload: domain.IssueUpdateInput{TeamID: &destination.ID, StateID: &destinationState.ID}},
						func(current *domain.Bootstrap, target *domain.Issue) error {
							capture := scaleP21SW239Callback{Team: target.Team.ID, Version: target.Version}
							for index := range current.Documents {
								if current.Documents[index].IssueID == target.ID {
									capture.Documents = append(capture.Documents, current.Documents[index])
									current.Documents[index].IssueTeamID = destination.ID
									current.Documents[index].InheritedTeamIDs = []string{destination.ID}
								}
							}
							callbacks = append(callbacks, capture)
							target.Team, target.State = destination, destinationState
							target.Title = "Retried committed move"
							return nil
						})
					finished := time.Now().UTC()
					if err != nil || len(callbacks) != 2 || len(callbacks[0].Documents) != 1 || len(callbacks[1].Documents) != 0 {
						var counts []int
						for _, callback := range callbacks {
							counts = append(counts, len(callback.Documents))
						}
						t.Fatal("required actual conflict retry success changed", len(callbacks), counts, err)
					}
					if callbacks[0].Documents[0].Content != document.Content {
						t.Fatal("first real mutation attempt lost the complete native legal-large document")
					}

					if result.Version != initial.Version+1 || result.Team.ID != destination.ID ||
						result.UpdatedAt.Before(started) || result.UpdatedAt.After(finished) {
						t.Fatal("retry committed more than once or returned the wrong issue", result)
					}
					if bounded && (logicalRefresh.Load() != 1 || literalRefresh.Load() != 0) ||
						!bounded && (literalRefresh.Load() != 1 || logicalRefresh.Load() != 0) {
						t.Fatal("actual UPDATE did not select the keyed refresher", literalRefresh.Load(), logicalRefresh.Load())
					}
					// The test never calls a refresh function: the query above
					// can run only after the production retry callback accepts
					// the actual metadataConflictError and its key set.
					if bounded {
						resident, _ := source.storedWorkspace(workspace)
						if len(resident.Documents) != 0 || len(resident.Projects) != 0 ||
							source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("logical retry hydrated resident records or retained work")
						}
					}
					native := map[string]any{
						"documents": scaleP21IssueNative(t, source, workspace, "documents"),
						"projects":  scaleP21IssueNative(t, source, workspace, "projects"),
					}
					documents := native["documents"].([]domain.Document)
					if len(documents) != 1 || documents[0].IssueID != other.ID || documents[0].IssueTeamID != peerTeam.ID ||
						documents[0].Content != document.Content {
						t.Fatal("retry overwrote the committed peer attachment or body", len(documents))
					}
					metadata := scaleP21SW76MetadataImage(t, source.db, workspace)
					if !reflect.DeepEqual(metadata, canonicalBefore) {
						t.Fatal("retry changed canonical metadata after peer reattachment")
					}
					events := scaleP21SW63NewEvents(t, source, workspace, priorEvents)
					publicationMu.Lock()
					hooks, pushes := slices.Clone(webhooks), slices.Clone(realtime)
					publicationMu.Unlock()
					if len(events) != 1 || len(hooks) != 1 || len(pushes) != 1 {
						t.Fatal("failed attempt published or successful retry did not publish once", len(events), len(hooks), len(pushes))
					}
					if hooks[0].ID != pushes[0].ID || !hooks[0].CreatedAt.Equal(pushes[0].CreatedAt) || hooks[0].CreatedAt.Before(result.UpdatedAt) {
						t.Fatal("publication identity or clock ordering diverged")
					}
					replacements := map[string]string{
						result.UpdatedAt.Format(time.RFC3339Nano):     "<sw258-clock>",
						result.UpdatedAt.Format(issueRecordTimestamp): "<sw258-clock>",
					}
					for _, event := range hooks {
						if event.ID == "" || event.CreatedAt.Before(started) || event.CreatedAt.After(finished) {
							t.Fatal("invalid committed publication identity/clock", event)
						}
						replacements[event.ID] = "<sw258-event-id>"
						replacements[event.CreatedAt.Format(time.RFC3339Nano)] = "<sw258-clock>"
					}
					for _, event := range pushes {
						if event.ID == "" || event.CreatedAt.Before(started) || event.CreatedAt.After(finished) {
							t.Fatal("invalid realtime identity/clock", event)
						}
						replacements[event.ID] = "<sw258-event-id>"
						replacements[event.CreatedAt.Format(time.RFC3339Nano)] = "<sw258-clock>"
					}
					output := scaleP21SW239Normalized(t, map[string]any{
						"returned":        result,
						"business":        scaleP21SW63BusinessRows(t, source, workspace, issue.ID),
						"indexes":         scaleP21SW98Indexes(t, source, workspace),
						"events":          events,
						"webhooks":        hooks,
						"webhookActor":    hooks[0].ActorID,
						"webhookSnapshot": json.RawMessage(hooks[0].Snapshot),
						"realtime":        pushes,
					}, replacements)
					if !bounded {
						wantOutput, wantCallbacks, wantNative, wantMetadata = output, callbacks, native, metadata
					} else if !reflect.DeepEqual(wantOutput, output) || !reflect.DeepEqual(wantCallbacks, callbacks) ||
						!reflect.DeepEqual(wantNative, native) || !reflect.DeepEqual(wantMetadata, metadata) {
						t.Fatal("complete literal/logical retry output, callback, native, SQL or publication differs")
					}
					if bounded && (source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0) {
						t.Fatal("retry inspection retained bounded work or decoded complete metadata")
					}
					t.Logf("bounded=%t callbacks=%d refreshes=%d/%d metadata_conflict=%v publications=%d/%d",
						bounded, len(callbacks), literalRefresh.Load(), logicalRefresh.Load(), keys, len(hooks), len(pushes))
				})
			}
		})
	}
}
