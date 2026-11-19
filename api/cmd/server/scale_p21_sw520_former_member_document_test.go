package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW520Outcome struct {
	// The literal socketWorkspaceMetadata + collaborationDocument read, or
	// its bounded form.
	// ContentData is compared as its JSON (an empty map is "null": the
	// field is omitempty, so an SQL-loaded copy of an empty map is nil, and
	// collectMentionIDs reads both alike) and as the mentions it holds.
	ReadError       string
	ContentState    string
	ContentData     string
	ContentMentions []string
	// The actual collaboration frame: refusal, socket output and storage.
	FrameError, PublicError string
	Messages                []string
	Stored                  bool
}

// TestScaleP21SW520FormerMemberDocumentDifferential compares the standalone
// document branch of refuseFormerMemberCollaborationUpdate (legacy
// socketWorkspaceMetadata versus the bounded single-document content read)
// through the actual collaboration frame: the content it reads, the refusal,
// the socket output and the stored update, for warm and SQL-reloaded bounded
// sources, over principals, credentials, development mode, generations,
// duplicate and conflicting collaboration IDs, hidden and missing documents,
// mentions kept in content data, state or pending updates, and malformed
// updates.
func TestScaleP21SW520FormerMemberDocumentDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21M2Setup(t, repo)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw520-")
			_, former := scaleP03Join(t, f.scaleP03PlatformFixture, "sw520-former", prefix+"former@example.test", "member", f.public)
			plain, stateKept, clean := prefix+"plain", prefix+"state-kept", prefix+"clean"
			generation, current := prefix+"generation", prefix+"current"
			duplicate, collision := prefix+"duplicate-current", prefix+"collision"
			pending, hidden, large := prefix+"pending", prefix+"hidden", prefix+"large"
			private := ""
			restore := store.ForceFullMutationsForTesting(true)
			err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw520_seed", "", nil, func(data *domain.Bootstrap) error {
				for _, team := range data.Teams {
					if strings.HasPrefix(team.ID, "m2-private-") {
						private = team.ID
					}
				}
				document := func(id, collaboration string, content map[string]any, state string) domain.Document {
					return domain.Document{
						ID: id, SlugID: id + "-slug", CollaborationID: collaboration, Title: "SW520 " + id,
						Content: "SW520 body", ContentData: content, ContentState: state,
						Creator: data.Viewer, TeamIDs: []string{f.public},
						Permissions: []domain.DocumentPermission{{SubjectType: "workspace", SubjectID: data.Workspace.ID, Role: "editor"}},
					}
				}
				paragraphs := []any{}
				for index := range 400 {
					paragraphs = append(paragraphs, map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("SW520 paragraph %03d %s", index, strings.Repeat("x", 200))}}})
				}
				paragraphs = append(paragraphs, mentioningDocument("tail", former.ID)["content"].([]any)...)
				secret := document(hidden, "", mentioningDocument("private", former.ID), "")
				secret.TeamIDs = []string{private}
				secret.Permissions = nil
				data.Documents = append(data.Documents,
					document(plain, "", mentioningDocument("kept", former.ID), ""),
					document(stateKept, "", nil, mentioningCollaborationState(former.ID)),
					document(clean, "", mentioningDocument("clean"), ""),
					document(generation, current, mentioningDocument("restored", former.ID), mentioningCollaborationState()),
					// The first duplicate keeps the mention; the second does not.
					document(prefix+"duplicate-first", duplicate, mentioningDocument("first", former.ID), ""),
					document(prefix+"duplicate-last", duplicate, mentioningDocument("last"), ""),
					// An earlier stale ID precedes a later current match.
					document(collision, prefix+"collision-generation", mentioningDocument("earlier", former.ID), ""),
					document(prefix+"collision-later", collision, mentioningDocument("later"), ""),
					document(pending, "", map[string]any{}, ""),
					secret,
					document(large, "", map[string]any{"type": "doc", "content": paragraphs}, ""),
				)
				return nil
			})
			restore()
			if err != nil || private == "" {
				t.Fatal("seed former-member documents", err)
			}
			mention, err := base64.StdEncoding.DecodeString(mentioningCollaborationState(former.ID))
			if err != nil {
				t.Fatal(err)
			}
			created, err := repo.AppendDocumentCollaborationUpdate(t.Context(), workspace, store.DocumentCollaborationUpdate{
				ID: "collab_" + prefix + "pending", DocumentID: pending, ClientID: "sw520-producer", Data: mention,
				CreatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
			})
			if err != nil || !created {
				t.Fatal("seed pending mention", created, err)
			}
			if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, former.ID); err != nil {
				t.Fatal(err)
			}
			noMention, err := base64.StdEncoding.DecodeString(mentioningCollaborationState())
			if err != nil {
				t.Fatal(err)
			}
			owner, admin, member, guest := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user
			emptyKey := &domain.APIKey{Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}}
			publicKey := &domain.APIKey{Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
			privateKey := &domain.APIKey{Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{private}}
			type row struct {
				name, ref   string
				user        domain.User
				key         *domain.APIKey
				development bool
				update      []byte
			}
			rows := []row{
				{"content-data-keeps-mention", plain, owner, nil, false, mention},
				{"state-keeps-mention", stateKept, member, nil, false, mention},
				{"new-mention-refused", clean, owner, nil, false, mention},
				{"no-mention", clean, member, nil, false, noMention},
				{"malformed-update", clean, owner, nil, false, []byte("not a collaboration update")},
				{"current-generation", current, owner, nil, false, mention},
				{"stale-id-conflict-still-read", generation, member, nil, false, mention},
				{"slug-conflict-still-read", generation + "-slug", owner, nil, false, mention},
				{"first-duplicate", duplicate, owner, nil, false, mention},
				{"earlier-stale-id-before-current", collision, owner, nil, false, mention},
				{"pending-update-keeps-mention", pending, guest, nil, false, mention},
				{"hidden-from-member", hidden, member, nil, false, mention},
				{"visible-to-private-admin", hidden, admin, nil, false, mention},
				{"selected-empty-key", plain, member, emptyKey, false, mention},
				{"selected-public-key", plain, member, publicKey, false, mention},
				{"selected-private-key", hidden, admin, privateKey, false, mention},
				{"development-unprojected", hidden, guest, emptyKey, true, mention},
				{"development-new-mention", clean, guest, nil, true, mention},
				{"missing-document", prefix + "missing", owner, nil, false, mention},
				{"large-content", large, member, nil, false, mention},
			}
			run := func(t *testing.T, selected *store.SQLiteStore, test row) scaleP21SW520Outcome {
				t.Helper()
				service := &server{store: selected, realtime: newRealtimeHub(), authDisabled: test.development}
				request := f.literalRequest(test.user, test.key, nil)
				request.Method, request.URL.Path = http.MethodGet, "/api/realtime/socket"
				var result scaleP21SW520Outcome
				if selected.BoundedMetadataEnabled() {
					read, err := service.beginFormerMemberDocumentContent(request, workspace, test.ref)
					if err != nil {
						result.ReadError = err.Error()
					} else {
						result.ContentState = read.Document.ContentState
						result.ContentData, result.ContentMentions = scaleP21SW520Content(t, read.Document.ContentData)
						read.Close()
					}
				} else {
					data, err := service.socketWorkspaceMetadata(request, workspace)
					if err == nil {
						var document domain.Document
						document, _, err = collaborationDocument(data, test.ref)
						if err == nil {
							result.ContentState = document.ContentState
							result.ContentData, result.ContentMentions = scaleP21SW520Content(t, document.ContentData)
						}
					}
					if err != nil {
						result.ReadError = err.Error()
					}
				}
				client := service.realtime.addSocket(workspace, "sw520", func() {})
				if client == nil {
					t.Fatal("socket admission failed")
				}
				client.userID = test.user.ID
				service.realtime.joinDocument(client, test.ref)
				updateID := "collab_" + prefix + test.name
				err := service.handleCollaborationFrame(request, client, encodeCollaborationFrame(collaborationUpdateFrame, test.ref, updateID, test.update))
				if err != nil {
					result.FrameError, result.PublicError = err.Error(), publicErrorMessage(request.Context(), err)
				}
				result.Messages = []string{}
				for len(client.send) > 0 {
					message := <-client.send
					result.Messages = append(result.Messages, base64.StdEncoding.EncodeToString(message.data))
					message.release(client)
				}
				service.realtime.removeSocket(client)
				updates, err := repo.DocumentCollaborationUpdates(t.Context(), workspace, test.ref)
				if err != nil {
					t.Fatal(err)
				}
				result.Stored = slices.ContainsFunc(updates, func(update store.DocumentCollaborationUpdate) bool { return update.ID == updateID })
				if result.Stored {
					if err := repo.DeleteDocumentCollaborationUpdates(t.Context(), workspace, test.ref, []string{updateID}); err != nil {
						t.Fatal(err)
					}
				}
				if service.realtime.budget.used.Load() != 0 {
					t.Fatal("collaboration frame retained realtime work", test.name)
				}
				if selected.BoundedMetadataEnabled() && (selected.MetadataWorkUsedForTesting() != 0 || selected.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("former-member document read retained work or hydrated metadata", test.name)
				}
				return result
			}
			refused, stored, notFound, conflicts := 0, 0, 0, 0
			for _, test := range rows {
				t.Run(test.name, func(t *testing.T) {
					want := run(t, repo, test)
					for _, mode := range []string{"warm", "reloaded"} {
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						if mode == "reloaded" {
							if err := f.probe.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
						}
						got := run(t, f.probe, test)
						if !reflect.DeepEqual(want, got) {
							t.Fatalf("%s former-member document changed:\n%s", mode, scaleP21M2Diff(want, got))
						}
					}
					if want.FrameError == errFormerMemberMention.Error() {
						refused++
					}
					if want.Stored {
						stored++
					}
					if want.ReadError == errNotFound.Error() {
						notFound++
					}
					switch test.name {
					case "content-data-keeps-mention", "state-keeps-mention", "pending-update-keeps-mention", "first-duplicate",
						"earlier-stale-id-before-current", "visible-to-private-admin", "development-unprojected", "large-content",
						"current-generation", "stale-id-conflict-still-read", "slug-conflict-still-read", "selected-public-key", "selected-private-key", "no-mention":
						if want.FrameError != "" || !want.Stored {
							t.Fatalf("legacy refused a kept mention: %+v", want)
						}
					case "new-mention-refused", "development-new-mention":
						if want.FrameError != errFormerMemberMention.Error() || want.Stored {
							t.Fatalf("legacy accepted a new former-member mention: %+v", want)
						}
					case "hidden-from-member", "missing-document", "selected-empty-key":
						if want.ReadError != errNotFound.Error() || want.Stored {
							t.Fatalf("legacy read a hidden or missing document: %+v", want)
						}
					}
					if test.name == "stale-id-conflict-still-read" || test.name == "slug-conflict-still-read" || test.name == "earlier-stale-id-before-current" {
						conflicts++
					}
				})
			}
			if refused == 0 || stored == 0 || notFound == 0 || conflicts != 3 {
				t.Fatalf("missing branch witnesses refused=%d stored=%d notFound=%d conflicts=%d", refused, stored, notFound, conflicts)
			}
			t.Logf("SW520 former-member standalone document rows=%d refused=%d stored=%d notFound=%d (legacy, warm, reloaded)", len(rows), refused, stored, notFound)
		})
	}
}

func scaleP21SW520Content(t *testing.T, content map[string]any) (string, []string) {
	t.Helper()
	mentions := []string{}
	collectMentionIDs(content, &mentions)
	if len(content) == 0 {
		return "null", mentions
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), mentions
}
