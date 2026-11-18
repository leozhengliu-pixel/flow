package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/Deln0r/ygo"
)

func scaleP21SW480DeletedMentionState(userID string) string {
	document := ygo.NewDoc()
	fragment := ygo.NewXmlFragment(document, "prosemirror")
	transaction := document.WriteTxn()
	mention := fragment.InsertXmlElement(transaction, 0, "mention")
	mention.SetAttribute(transaction, "id", userID)
	mention.SetAttribute(transaction, "mentionType", "user")
	transaction.Commit()
	transaction = document.WriteTxn()
	fragment.Delete(transaction, 0, 1)
	transaction.Commit()
	return base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(document))
}

func scaleP21SW480CanonicalDocument(t *testing.T, reader *scaleP04Database, repo *store.SQLiteStore, workspace, id string) (domain.Document, json.RawMessage) {
	t.Helper()
	rows := reader.query(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' ORDER BY collection_order,record_key`, workspace)
	defer rows.Close()
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		var recordID string
		if err := json.Unmarshal(record["id"], &recordID); err != nil {
			t.Fatal(err)
		}
		if recordID != id {
			continue
		}
		// The current-version marker refers to this row's current body;
		// older object/blob references remain real OffloadedContent values.
		var revisions []map[string]json.RawMessage
		if err := json.Unmarshal(record["revisions"], &revisions); err != nil && string(record["revisions"]) != "null" && len(record["revisions"]) != 0 {
			t.Fatal(err)
		}
		for _, revision := range revisions {
			var marker map[string]json.RawMessage
			if json.Unmarshal(revision["_offloaded"], &marker) != nil || string(marker["body"]) != `"document"` {
				continue
			}
			for _, name := range []string{"content", "contentState", "contentData"} {
				if value, found := record[name]; found {
					revision[name] = value
				} else {
					delete(revision, name)
				}
			}
			delete(revision, "_offloaded")
		}
		record["revisions"], _ = json.Marshal(revisions)
		expanded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		var document domain.Document
		if err := json.Unmarshal(expanded, &document); err != nil {
			t.Fatal(err)
		}
		if err := repo.LoadDocumentRevisionBodies(t.Context(), workspace, document.Revisions); err != nil {
			t.Fatal(err)
		}
		return document, raw
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Duplicate document IDs share one record key; the literal full writer
	// can leave no canonical row for them.
	return domain.Document{}, nil
}

func scaleP21SW480Installed(t *testing.T, repo *store.SQLiteStore, workspace string) map[string]json.RawMessage {
	t.Helper()
	names := []string{"documents", "projects", "favorites"}
	result := map[string]json.RawMessage{}
	if repo.BoundedMetadataEnabled() {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("document postimage missing", err)
		}
		defer read.Close()
		for _, name := range names {
			var output strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &output); err != nil {
				t.Fatal(err)
			}
			result[name] = json.RawMessage(output.String())
		}
		return result
	}
	data, found := repo.WorkspaceMetadataFields(workspace, names...)
	if !found {
		t.Fatal("legacy document postimage missing")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		result[name] = fields[name]
	}
	return result
}

func scaleP21SW480Normalize(t *testing.T, value any, fields map[string]json.RawMessage, documentID string, existing map[string]bool, started, finished time.Time) any {
	t.Helper()
	ids := map[string]string{}
	var documents []domain.Document
	if err := json.Unmarshal(fields["documents"], &documents); err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if document.ID != documentID {
			continue
		}
		if document.CollaborationID != "" && !existing[document.CollaborationID] {
			ids[document.CollaborationID] = "<generated-collaboration>"
		}
		for index, revision := range document.Revisions {
			if !existing[revision.ID] {
				ids[revision.ID] = fmt.Sprintf("<generated-revision:%d>", index)
			}
		}
	}
	var favorites []domain.Favorite
	if err := json.Unmarshal(fields["favorites"], &favorites); err != nil {
		t.Fatal(err)
	}
	for index, favorite := range favorites {
		if !existing[favorite.ID] {
			ids[favorite.ID] = fmt.Sprintf("<generated-favorite:%d>", index)
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var normalize func(any, string) any
	normalize = func(value any, key string) any {
		switch value := value.(type) {
		case string:
			if replacement, found := ids[value]; found {
				return replacement
			}
			if strings.EqualFold(key, "startedAt") || strings.EqualFold(key, "archivedAt") {
				if clock, err := time.Parse(time.RFC3339Nano, value); err == nil && !clock.Before(started) && !clock.After(finished) {
					return "<generated-clock>"
				}
			}
		case map[string]any:
			for key, child := range value {
				value[key] = normalize(child, key)
			}
		case []any:
			for index, child := range value {
				value[index] = normalize(child, key)
			}
		}
		return value
	}
	return normalize(result, "")
}

// scaleP21SW480Path names the first differing JSON path of two normalized
// outcomes.
func scaleP21SW480Path(left, right any, path string) string {
	switch a := left.(type) {
	case map[string]any:
		b, ok := right.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: kind %T vs %T", path, left, right)
		}
		keys := make([]string, 0, len(a)+len(b))
		for key := range a {
			keys = append(keys, key)
		}
		for key := range b {
			if _, found := a[key]; !found {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			if !reflect.DeepEqual(a[key], b[key]) {
				return scaleP21SW480Path(a[key], b[key], path+"."+key)
			}
		}
	case []any:
		b, ok := right.([]any)
		if !ok || len(a) != len(b) {
			raw, _ := json.Marshal(right)
			expected, _ := json.Marshal(left)
			return fmt.Sprintf("%s: legacy=%.600s bounded=%.600s", path, expected, raw)
		}
		for index := range a {
			if !reflect.DeepEqual(a[index], b[index]) {
				return scaleP21SW480Path(a[index], b[index], fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	expected, _ := json.Marshal(left)
	raw, _ := json.Marshal(right)
	return fmt.Sprintf("%s: legacy=%.600s bounded=%.600s", path, expected, raw)
}

func TestScaleP21SW480DocumentUpdateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	peer, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw480-")
	reader := &scaleP04Database{config: config, key: workspace, workspaceID: f.data.Workspace.ID}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	sqlFixture := scaleP21SW10Fixture{reader: reader}
	sqlFixture.data.Workspace.URLKey = workspace
	documentID, oldProject, newProject, reciprocal := prefix+"document", prefix+"old-project", prefix+"new-project", prefix+"reciprocal-project"
	privateTeam := ""
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "SW480 linked issue", "teamId": f.public}, workspace, http.StatusCreated)
	outsider := domain.User{ID: prefix + "outsider", Name: "Outsider", Email: prefix + "outsider@example.test", Active: true}
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	oldState := mentioningCollaborationState(f.data.Viewer.ID)
	compactedState := mentioningCollaborationState(f.data.Viewer.ID)
	formerID := f.principals[2].user.ID
	formerState := mentioningCollaborationState(formerID)
	// Yjs states carry random client IDs: build every fixture state once so
	// all four modes start from the same bytes.
	retainedState, deletedState := mentioningCollaborationState(formerID), scaleP21SW480DeletedMentionState(formerID)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw480_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{{
			ID: documentID, SlugID: "original-" + prefix + "0123456789ab", PreviousSlugIDs: []string{prefix + "old"},
			Title: "Original document", Content: "Original body", ContentState: oldState,
			ContentData: map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Original body"}}}}},
			Creator:     data.Viewer, Version: 7, ProjectIDs: []string{oldProject}, TeamIDs: []string{f.public},
			CollaborationID: prefix + "collaboration", CreatedAt: clock, UpdatedAt: clock,
			SubscriberIDs: []string{f.principals[2].user.ID},
			Permissions:   []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "editor"}},
		}}
		data.Projects = append(data.Projects,
			domain.Project{ID: oldProject, Name: "Before", TeamIDs: []string{f.public}, Description: strings.Repeat("linked project body <>& ", 16000), Resources: []domain.ProjectResource{{ID: documentID, Title: "Original document", URL: "/before"}}},
			domain.Project{ID: newProject, Name: "After", TeamIDs: []string{f.public}, Description: strings.Repeat("new project body ", 24000), Resources: []domain.ProjectResource{}},
			domain.Project{ID: reciprocal, Name: "Reciprocal", Description: strings.Repeat("reciprocal body ", 24000), Resources: []domain.ProjectResource{{ID: prefix + "other-resource", Title: "Kept"}, {ID: documentID, Title: "Stale reciprocal", URL: "/stale"}}},
		)
		data.Favorites = nil
		data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: prefix + "subscription", ResourceType: "document", ResourceID: documentID, UserID: f.principals[2].user.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned document fixture missing")
	}
	for _, team := range baseline.Teams {
		if team.Private && strings.HasPrefix(team.ID, "m2-private-") {
			privateTeam = team.ID
		}
	}
	if privateTeam == "" {
		t.Fatal("private team fixture missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	existing := map[string]bool{
		prefix + "collaboration": true, prefix + "latest": true,
		prefix + "older": true, prefix + "oldest": true,
	}
	for _, document := range baseline.Documents {
		for _, revision := range document.Revisions {
			existing[revision.ID] = true
		}
	}
	tests := []struct {
		name, ref string
		body      map[string]any
	}{
		{"title-and-baseline", documentID, map[string]any{"title": "Renamed document"}},
		{"markdown-generation", documentID, map[string]any{"content": "Replacement Markdown"}},
		{"rich-content", documentID, map[string]any{"contentData": mentioningDocument("New rich body", f.principals[2].user.ID)}},
		{"favorite-from-nil", documentID, map[string]any{"favorite": true}},
		{"no-op", documentID, map[string]any{}},
		{"version-conflict", documentID, map[string]any{"title": "Rejected", "expectedVersion": 6}},
		{"stale-content-version", documentID, map[string]any{"contentState": mentioningCollaborationState(f.principals[2].user.ID), "expectedContentVersion": 99}},
		{"accepted-content-state", documentID, map[string]any{"contentState": mentioningCollaborationState(f.principals[2].user.ID), "expectedContentVersion": 0}},
		{"project-links", documentID, map[string]any{"projectIds": []string{newProject}}},
		{"subscriber-change", documentID, map[string]any{"subscriberIds": []string{f.principals[3].user.ID}}},
		{"old-slug", prefix + "old", map[string]any{"title": "Edited through old slug"}},
		{"missing", prefix + "missing", map[string]any{"title": "Missing"}},
		{"compaction", documentID, map[string]any{"content": "Compacted body", "contentState": compactedState, "expectedContentVersion": 0, "documentUpdateIds": []string{prefix + "prune"}}},
		{"compaction-stale", documentID, map[string]any{"content": "Independent stale-base body", "contentState": formerState, "expectedContentVersion": 0, "documentUpdateIds": []string{prefix + "prune"}}},
		{"former-retained-rich", documentID, map[string]any{"contentData": mentioningDocument("Retained former mention", formerID)}},
		{"former-retained-state", documentID, map[string]any{"contentState": formerState, "expectedContentVersion": 0}},
		{"former-retained-queued", documentID, map[string]any{"contentData": mentioningDocument("Queued former mention", formerID)}},
		{"former-invalid-before-rich-retained", documentID, map[string]any{"contentData": mentioningDocument("Retained rich legacy evidence", formerID)}},
		{"former-reject-new-rich", documentID, map[string]any{"contentData": mentioningDocument("Rejected former mention", formerID)}},
		{"former-reject-new-state", documentID, map[string]any{"contentState": formerState, "expectedContentVersion": 0, "documentUpdateIds": []string{prefix + "prune"}}},
		{"former-reject-after-compaction", documentID, map[string]any{"contentState": compactedState, "expectedContentVersion": 0, "documentUpdateIds": []string{prefix + "prune"}, "contentData": mentioningDocument("Rejected after accepted base", formerID)}},
		{"former-reject-deleted-history", documentID, map[string]any{"contentState": formerState, "expectedContentVersion": 0}},
		{"former-remove-mention", documentID, map[string]any{"contentData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}}},
		{"offloaded-history", documentID, map[string]any{"title": "Preserve offloaded history"}},
		{"offloaded-history-summary", documentID, map[string]any{"title": "Summary response after edit"}},
		{"large-current-and-history", documentID, map[string]any{"title": "Large current body retained"}},
		{"large-current-content-edit", documentID, map[string]any{"content": strings.Repeat("y", 4<<20)}},
		{"icon-color", documentID, map[string]any{"icon": "rocket", "color": "#ff0000"}},
		{"archive", documentID, map[string]any{"archived": true}},
		{"unarchive", documentID, map[string]any{"archived": false}},
		{"trashed", prefix + "trashed", map[string]any{"title": "Trashed"}},
		{"team-move", documentID, map[string]any{"teamIds": []string{privateTeam}}},
		{"issue-link", documentID, map[string]any{"issueId": issue.ID}},
		{"issue-link-invalid", documentID, map[string]any{"issueId": prefix + "no-issue"}},
		{"project-invalid", documentID, map[string]any{"projectIds": []string{prefix + "no-project"}}},
		{"projects-with-existing-issue", documentID, map[string]any{"projectIds": []string{newProject, oldProject}}},
		{"unlink-all", documentID, map[string]any{"projectIds": []string{}, "issueId": ""}},
		{"subscriber-invalid", documentID, map[string]any{"subscriberIds": []string{prefix + "nobody"}}},
		{"favorite-remove", documentID, map[string]any{"favorite": false}},
		{"slug-over-earlier-old-slug", "original-" + prefix + "0123456789ab", map[string]any{"title": "Slug wins"}},
		{"id-over-earlier-old-slug", documentID, map[string]any{"title": "ID wins"}},
		{"duplicate-first", documentID, map[string]any{"title": "First duplicate edited"}},
		{"duplicate-later-slug", prefix + "duplicate-slug", map[string]any{"title": "Later duplicate edited"}},
		{"expected-version-match", documentID, map[string]any{"title": "Matching version", "expectedVersion": 7}},
		{"peer-merge", documentID, map[string]any{"title": "After peer"}},
		{"peer-conflict-retry", documentID, map[string]any{"title": "After peer"}},
		{"peer-large-merge", documentID, map[string]any{"title": "After peer"}},
	}
	formerSuspended := false
	run := func(t *testing.T, testName, ref string, body map[string]any, principal domain.User, credential *domain.APIKey) {
		t.Helper()
		var expected any
		for _, mode := range []string{"legacy-warm", "bounded-warm", "legacy-reloaded", "bounded-reloaded"} {
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			if formerSuspended {
				if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, formerID); err != nil {
					t.Fatal(err)
				}
				formerSuspended = false
			}
			restore := store.ForceFullMutationsForTesting(true)
			err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw480_reset", "", nil, func(data *domain.Bootstrap) error {
				var restored domain.Bootstrap
				if err := json.Unmarshal(saved, &restored); err != nil {
					return err
				}
				*data = restored
				document := &data.Documents[0]
				switch testName {
				case "compaction-stale":
					document.ContentVersion = 1
				case "former-retained-rich", "former-invalid-before-rich-retained", "former-remove-mention":
					document.ContentData = mentioningDocument("Old former mention", formerID)
					if testName == "former-invalid-before-rich-retained" {
						document.ContentState = "invalid-before-state"
					}
				case "former-retained-state":
					document.ContentData = nil
					document.ContentState = retainedState
				case "former-retained-queued", "former-reject-new-rich", "former-reject-new-state", "former-reject-after-compaction":
					document.ContentData = nil
				case "former-reject-deleted-history":
					document.ContentData = nil
					document.ContentState = deletedState
				case "unarchive":
					document.ArchivedAt = &clock
				case "permission-private":
					document.TeamIDs, document.Permissions = []string{privateTeam}, nil
				case "trashed":
					trashed := *document
					trashed.ID, trashed.SlugID, trashed.PreviousSlugIDs = prefix+"trashed", "trashed-"+prefix, nil
					payload, err := json.Marshal(trashed)
					if err != nil {
						return err
					}
					data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "document", ResourceID: trashed.ID, Title: trashed.Title, Payload: payload, DeletedBy: baseline.Viewer, DeletedAt: clock, ExpiresAt: clock.Add(time.Hour * 24 * 365 * 10)})
				case "projects-with-existing-issue":
					document.IssueID = issue.ID
				case "favorite-remove":
					document.Favorite = true
					data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + "favorite", UserID: baseline.Viewer.ID, ResourceType: "document", ResourceID: documentID, Position: -1, CreatedAt: clock})
				case "slug-over-earlier-old-slug", "id-over-earlier-old-slug":
					other := domain.Document{ID: prefix + "earlier", SlugID: "earlier-" + prefix, PreviousSlugIDs: []string{"original-" + prefix + "0123456789ab", documentID}, Title: "Earlier alias holder", Creator: baseline.Viewer, Version: 3, CreatedAt: clock, UpdatedAt: clock}
					data.Documents = append([]domain.Document{other}, data.Documents...)
				case "duplicate-first", "duplicate-later-slug":
					duplicate := *document
					duplicate.SlugID, duplicate.Title, duplicate.Version = prefix+"duplicate-slug", "Later duplicate", 2
					data.Documents = append(data.Documents, duplicate)
				case "offloaded-history", "offloaded-history-summary", "large-current-and-history", "large-current-content-edit", "peer-large-merge":
					if testName == "large-current-and-history" || testName == "large-current-content-edit" || testName == "peer-large-merge" {
						document.Content = strings.Repeat("x", 4<<20)
					}
					document.Revisions = []domain.DocumentRevision{
						{ID: prefix + "latest", DocumentID: documentID, Title: document.Title, Content: document.Content, ContentState: document.ContentState, ContentData: document.ContentData, Author: baseline.Viewer, CreatedAt: clock, StartedAt: &clock},
						{ID: prefix + "older", DocumentID: documentID, Title: "Offloaded old body", Content: strings.Repeat("old body <>& ", 8192), Author: baseline.Viewer, CreatedAt: clock.Add(-time.Hour)},
						{ID: prefix + "oldest", DocumentID: documentID, Title: "Oldest body", Content: strings.Repeat("oldest text ", 8192), Author: baseline.Viewer, CreatedAt: clock.Add(-2 * time.Hour)},
					}
				}
				return nil
			})
			restore()
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(testName, "former-") {
				if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, formerID); err != nil {
					t.Fatal(err)
				}
				formerSuspended = true
			}
			collaborationID := prefix + "collaboration"
			if err := repo.DeleteDocumentCollaborationDocument(t.Context(), workspace, collaborationID); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"prune", "keep"} {
				state := oldState
				if testName == "former-retained-queued" && name == "keep" {
					state = formerState
				}
				raw, err := base64.StdEncoding.DecodeString(state)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repo.AppendDocumentCollaborationUpdate(t.Context(), workspace, store.DocumentCollaborationUpdate{
					ID: prefix + name, DocumentID: collaborationID, Data: raw, ClientID: prefix + "client", CreatedAt: clock,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(mode, "reloaded") {
				// Force a real SQL decode (with its load normalization) in
				// both reloaded modes, whoever wrote the reset last.
				if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			source := repo
			if strings.HasPrefix(mode, "bounded") {
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				source = f.probe
			}
			if strings.HasPrefix(testName, "peer-") {
				// Another server edits the same document and linked project
				// rows after both sources installed their images. Both
				// writers start stale; peer-merge touches other members,
				// peer-conflict-retry the same title, which conflicts and
				// retries on the refreshed rows.
				if err := peer.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
				if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := peer.MutateWorkspace(t.Context(), workspace, "scale_p21.sw480_peer", "", nil, func(data *domain.Bootstrap) error {
					for index := range data.Documents {
						if data.Documents[index].ID == documentID {
							data.Documents[index].Icon = "peer-icon"
							if testName == "peer-conflict-retry" {
								data.Documents[index].Title = "Peer title"
							}
						}
					}
					for index := range data.Projects {
						if data.Projects[index].ID == oldProject {
							data.Projects[index].Name = "Peer renamed"
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			memberDirectories.Delete(fmt.Sprintf("%p:%s", source, f.data.Workspace.ID))
			service := &server{store: source, realtime: newRealtimeHub(), uploadPath: t.TempDir()}
			before := scaleP21SW59SQL(t, sqlFixture)
			_, beforeCanonical := scaleP21SW480CanonicalDocument(t, reader, repo, workspace, documentID)
			prior, err := repo.Events(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			priorIDs := map[string]bool{}
			for _, event := range prior {
				priorIDs[event.ID] = true
			}
			priorAudits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: documentID})
			if err != nil {
				t.Fatal(err)
			}
			auditIDs := map[string]bool{}
			for _, audit := range priorAudits {
				auditIDs[audit.ID] = true
			}
			got := scaleP21SW59Outcome{}
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
			})
			captureRealtime := func(_ string, event domain.RealtimeEvent) {
				got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
			}
			source.SetRealtimeSink(captureRealtime)
			if source != repo {
				repo.SetRealtimeSink(captureRealtime)
			}
			started := time.Now().UTC()
			statements := 0
			serve := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				request := f.literalRequest(principal, credential, body)
				request.Method, request.URL.Path = http.MethodPatch, "/api/documents/"+ref
				request.SetPathValue("id", ref)
				if testName == "offloaded-history-summary" {
					request.URL.RawQuery = "history=summary"
				}
				ctx, closeOutput := beginMetadataOutputReads(request.Context())
				defer closeOutput()
				request = request.WithContext(store.WithSQLStatementObserver(ctx, func(string) { statements++ }))
				// Session requests carry no cookie in this harness; key
				// requests run the real workspace authorization first.
				if credential == nil || service.authorizeWorkspaceRequest(response, request, principal) {
					service.updateDocument(response, request)
				}
				return response
			}
			response := serve()
			if testName == "compaction" {
				replay := serve()
				if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), response.Body.Bytes()) {
					t.Fatal("same compaction retry changed its successful result", mode, replay.Code, replay.Body.String())
				}
			}
			finished := time.Now().UTC()
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
			repo.SetRealtimeSink(nil)
			if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
				t.Fatal("document response leaked work or decoded complete workspace", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
			}
			fields := scaleP21SW480Installed(t, source, workspace)
			got.Code, got.Header, got.Rows = response.Code, response.Header(), scaleP21SW59SQL(t, sqlFixture)
			after, err := repo.Events(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range after {
				if !priorIDs[event.ID] {
					got.Events = append(got.Events, event)
				}
			}
			audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: documentID})
			if err != nil {
				t.Fatal(err)
			}
			newAudits := []domain.AuditLogEntry{}
			for _, audit := range audits {
				if !auditIDs[audit.ID] {
					audit.ID, audit.Seq, audit.Hash = "<generated-audit>", 0, ""
					newAudits = append(newAudits, audit)
				}
			}
			canonical, canonicalRaw := scaleP21SW480CanonicalDocument(t, reader, repo, workspace, documentID)
			updates, err := repo.DocumentCollaborationUpdates(t.Context(), workspace, collaborationID)
			if err != nil {
				t.Fatal(err)
			}
			updateState := []any{}
			for _, update := range updates {
				updateState = append(updateState, []any{update.ID, update.ClientID, update.Data, update.CreatedAt})
			}
			got.Response, err = json.Marshal(map[string]any{
				"response": json.RawMessage(response.Body.Bytes()), "installed": fields, "audits": newAudits,
				"canonical": canonical, "canonicalRaw": canonicalRaw, "updates": updateState,
			})
			if err != nil {
				t.Fatal(err)
			}
			normalized := scaleP21SW480Normalize(t, scaleP21SW59Normalized(t, got, before, started, finished), fields, documentID, existing, started, finished)
			if strings.HasPrefix(mode, "legacy") {
				expected = normalized
			} else if testName == "peer-large-merge" {
				// Known root gate, pinned rather than certified: a stale
				// image of a document row whose current and history bodies
				// exceed the large-row typed merge allowance (here 12 MiB
				// native) is refused atomically, while the literal writer
				// merges the peer's members. No row, event, audit or
				// publication changes and every admitted owner is released.
				if response.Code != http.StatusInternalServerError || len(got.Events) != 0 || len(got.Realtime) != 0 ||
					len(got.Webhooks) != 0 || len(newAudits) != 0 || !bytes.Equal(beforeCanonical, canonicalRaw) {
					t.Fatal("large stale document merge was not refused atomically", mode, response.Code, len(got.Events), len(newAudits))
				}
				t.Logf("document case=%s mode=%s SQL=%d status=%d (root gate)", testName, mode, statements, response.Code)
				continue
			} else if !reflect.DeepEqual(expected, normalized) {
				t.Fatalf("document update differs: case=%s mode=%s principal=%s status=%d: %s", testName, mode, principal.ID, response.Code, scaleP21SW480Path(expected, normalized, ""))
			}
			if testName == "version-conflict" && response.Code != http.StatusConflict {
				t.Fatal("conflict fixture did not reach captured document output", response.Code, response.Body.String())
			}
			if !strings.HasPrefix(testName, "permission-") {
				expectedCode := http.StatusOK
				switch testName {
				case "version-conflict":
					expectedCode = http.StatusConflict
				case "issue-link-invalid", "project-invalid", "subscriber-invalid":
					expectedCode = http.StatusBadRequest
				case "trashed":
					expectedCode = http.StatusNotFound
				}
				if strings.HasPrefix(testName, "former-reject-") {
					expectedCode = http.StatusBadRequest
				}
				if testName == "missing" {
					if response.Code != http.StatusNotFound && response.Code != http.StatusForbidden {
						t.Fatal("missing document returned an internal failure", response.Code, response.Body.String())
					}
				} else if response.Code != expectedCode {
					t.Fatal("required document branch did not succeed/refuse as expected", testName, mode, response.Code, expectedCode, response.Body.String())
				}
				noWrite := testName == "no-op" || testName == "stale-content-version" || testName == "missing" ||
					testName == "version-conflict" || strings.HasPrefix(testName, "former-reject-") || expectedCode != http.StatusOK
				wantVersion := int64(8)
				if strings.HasPrefix(testName, "duplicate-") {
					// Duplicate IDs share one canonical record key; the
					// compared outcome above is the proof.
					wantVersion = 0
				}
				if noWrite {
					if len(got.Events) != 0 || len(newAudits) != 0 || !bytes.Equal(beforeCanonical, canonicalRaw) {
						t.Fatal("refused/no-op document changed rows/events/audits", testName, len(got.Events), len(newAudits))
					}
				} else if len(got.Events) != 1 || len(newAudits) != 1 || wantVersion != 0 && canonical.Version != wantVersion {
					t.Fatal("successful document update lost its atomic write", testName, len(got.Events), len(newAudits), canonical.Version)
				}
			} else if response.Code != http.StatusOK && response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
				t.Fatal("permission matrix compared an internal failure", mode, response.Code, response.Body.String())
			}
			wantUpdates := 2
			if testName == "large-current-content-edit" {
				wantUpdates = 0
			}
			if strings.HasPrefix(testName, "duplicate-") {
				wantUpdates = len(updates)
			}
			if testName == "markdown-generation" {
				wantUpdates = 0
				if canonical.CollaborationID == collaborationID || canonical.ContentState != "" || canonical.Content != "Replacement Markdown" {
					t.Fatal("Markdown replacement did not install a fresh generation")
				}
			}
			if testName == "compaction" {
				wantUpdates = 1
				if canonical.ContentState != compactedState || canonical.ContentVersion != 1 || len(updates) != 1 || updates[0].ID != prefix+"keep" {
					t.Fatal("compaction state/version/precise log deletion changed", canonical.ContentVersion, updates)
				}
			}
			if testName == "compaction-stale" && (canonical.ContentVersion != 1 || canonical.ContentState != oldState || canonical.Content != "Independent stale-base body") {
				t.Fatal("stale compaction overwrote the base or lost the independent body")
			}
			if len(updates) != wantUpdates {
				t.Fatal("document save pruned the wrong collaboration updates", testName, len(updates), wantUpdates)
			}
			if testName == "large-current-content-edit" && (canonical.Content != strings.Repeat("y", 4<<20) || len(canonical.Revisions) == 0) {
				t.Fatal("large current content edit lost its body or revision")
			}
			if strings.HasPrefix(testName, "peer-") && (canonical.Icon != "peer-icon" || canonical.Title != "After peer") {
				t.Fatal("peer edit and retried update did not both persist", canonical.Icon, canonical.Title)
			}
			if strings.HasPrefix(testName, "offloaded-history") || testName == "large-current-and-history" {
				for _, revisionID := range []string{prefix + "older", prefix + "oldest"} {
					index := slices.IndexFunc(canonical.Revisions, func(revision domain.DocumentRevision) bool { return revision.ID == revisionID })
					if index < 0 || canonical.Revisions[index].Content == "" || len(canonical.Revisions[index].Offloaded) != 0 {
						t.Fatal("offloaded history body was lost or unreadable", revisionID)
					}
				}
				if !bytes.Contains(canonicalRaw, []byte(`"_offloaded"`)) {
					t.Fatal("offloaded history fixture never exercised stored references")
				}
				if testName == "large-current-and-history" && canonical.Content != strings.Repeat("x", 4<<20) {
					t.Fatal("large current body was truncated")
				}
				if testName == "offloaded-history-summary" {
					var body map[string]json.RawMessage
					if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					var headers []map[string]json.RawMessage
					if err := json.Unmarshal(body["revisions"], &headers); err != nil || len(headers) != len(canonical.Revisions) {
						t.Fatal("summary response lost revision headers", err, len(headers))
					}
					for _, header := range headers {
						if _, loaded := header["content"]; loaded || string(header["_bodyOmitted"]) != "true" {
							t.Fatal("summary response leaked revision bodies")
						}
					}
				} else {
					var document domain.Document
					if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
						t.Fatal(err)
					}
					for _, id := range []string{prefix + "older", prefix + "oldest"} {
						index := slices.IndexFunc(document.Revisions, func(revision domain.DocumentRevision) bool { return revision.ID == id })
						expected := slices.IndexFunc(canonical.Revisions, func(revision domain.DocumentRevision) bool { return revision.ID == id })
						if index < 0 || document.Revisions[index].Content != canonical.Revisions[expected].Content {
							t.Fatal("legacy document response did not load a complete offloaded body", id)
						}
					}
				}
			}
			if testName == "favorite-from-nil" && response.Code == http.StatusOK {
				var favorites []domain.Favorite
				if err := json.Unmarshal(fields["favorites"], &favorites); err != nil || len(favorites) != 1 {
					t.Fatal("favorite append lost nonnil collection shape", err, len(favorites))
				}
			}
			t.Logf("document case=%s mode=%s SQL=%d status=%d", testName, mode, statements, response.Code)
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { run(t, test.name, test.ref, test.body, baseline.Viewer, nil) })
	}
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		run(t, "permission-matrix", documentID, map[string]any{"title": "Permission matrix edit"}, user, key)
	})
	t.Run("permission-private", func(t *testing.T) {
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			run(t, "permission-private", documentID, map[string]any{"title": "Private team edit", "projectIds": []string{newProject}}, user, key)
		})
	})
	t.Run("permission-outsider", func(t *testing.T) {
		for _, key := range []*domain.APIKey{nil, {ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}} {
			run(t, "permission-outsider", documentID, map[string]any{"title": "Outsider edit"}, outsider, key)
		}
	})
}
