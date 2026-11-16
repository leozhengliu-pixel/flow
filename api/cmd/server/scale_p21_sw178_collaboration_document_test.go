package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW178SocketOutcome struct {
	Role, RoleError, JoinError string
	Conflict, Joined, CanEdit  bool
	AccessDocument             string
	Messages                   []string
	RecheckJoined              bool
	RecheckMessages            []string
}

type scaleP21SW178SocketCase struct {
	name, workspace, ref string
	user                 domain.User
	key                  *domain.APIKey
	development          bool
	revoked, invalid     bool
	writerFailure        bool
}

func scaleP21SW178SocketRun(t *testing.T, selected *store.SQLiteStore, f scaleP21M2Fixture, test scaleP21SW178SocketCase) scaleP21SW178SocketOutcome {
	t.Helper()
	service := &server{store: selected, realtime: newRealtimeHub(), authDisabled: test.development}
	request := f.literalRequest(test.user, test.key, nil)
	request.Method, request.URL.Path = http.MethodGet, "/api/realtime/socket"
	request.Header.Set("X-Workspace-Key", test.workspace)
	request.SetPathValue("workspaceKey", test.workspace)
	request.SetPathValue("workspace", test.workspace)
	var result scaleP21SW178SocketOutcome
	role, conflict, err := service.socketDocumentRole(request, test.workspace, test.ref)
	result.Role, result.Conflict = role, conflict
	if err != nil {
		result.RoleError = err.Error()
	}
	if selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("standalone role retained its read", test.name)
	}
	client := service.realtime.addSocket(test.workspace, "sw178-join", func() {})
	if client == nil {
		t.Fatal("socket admission failed")
	}
	client.userID, client.credentialRevoked = test.user.ID, test.revoked
	client.checkCredential = func() bool { return !test.invalid }
	type drained struct {
		messages               []string
		badJSON, releasedEarly bool
	}
	stop, finished := make(chan struct{}), make(chan drained, 1)
	go func() {
		state := drained{messages: []string{}}
		consume := func(message realtimeSocketMessage) {
			state.badJSON = state.badJSON || !json.Valid(message.data)
			state.messages = append(state.messages, string(message.data))
			if message.sent != nil && selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() == 0 {
				state.releasedEarly = true
			}
			message.release(client)
			if message.sent != nil {
				if test.writerFailure {
					message.sent <- errors.New("sw178 socket writer unavailable")
				} else {
					message.sent <- nil
				}
			}
		}
		for {
			select {
			case message := <-client.send:
				consume(message)
			case <-stop:
				for {
					select {
					case message := <-client.send:
						consume(message)
					default:
						finished <- state
						return
					}
				}
			}
		}
	}()
	command, err := json.Marshal(map[string]string{"type": "document.join", "documentId": test.ref})
	if err != nil {
		t.Fatal(err)
	}
	err = service.handleCollaborationCommand(request, client, command)
	if err != nil {
		result.JoinError = err.Error()
	}
	close(stop)
	messages := <-finished
	if messages.badJSON || messages.releasedEarly {
		t.Fatalf("socket output invalid or read released before writer acknowledgement: %s %+v", test.name, messages)
	}
	result.Messages = messages.messages
	service.realtime.mu.Lock()
	_, result.Joined = client.documents[test.ref]
	if access := client.documentAccess[test.ref]; access != nil {
		result.AccessDocument, result.CanEdit = access.documentID, access.canEdit
	}
	service.realtime.mu.Unlock()
	service.realtime.removeSocket(client)

	// A stale cached editor must follow the actual scalar role selector's
	// current decision, including access notices and revocation.
	recheck := service.realtime.addSocket(test.workspace, "sw178-recheck", func() {})
	if recheck == nil {
		t.Fatal("recheck socket admission failed")
	}
	recheck.documentAccess[test.ref] = &documentSocketAccess{documentID: result.AccessDocument, canEdit: true}
	recheck.checkDocument = func(ref string) (string, bool, error) {
		return service.socketDocumentRole(request, test.workspace, ref)
	}
	service.realtime.joinDocument(recheck, test.ref)
	service.realtime.recheckStandaloneDocument(recheck, test.ref)
	result.RecheckMessages = []string{}
	for len(recheck.send) > 0 {
		message := <-recheck.send
		if !json.Valid(message.data) {
			t.Fatal("invalid recheck socket output", test.name)
		}
		result.RecheckMessages = append(result.RecheckMessages, string(message.data))
		message.release(recheck)
	}
	result.RecheckJoined = service.realtime.joinedDocument(recheck, test.ref)
	service.realtime.removeSocket(recheck)
	if service.realtime.budget.used.Load() != 0 {
		t.Fatal("socket output retained realtime work", test.name)
	}
	if selected.BoundedMetadataEnabled() && (selected.MetadataWorkUsedForTesting() != 0 || selected.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("socket document lookup retained work or hydrated metadata", test.name)
	}
	return result
}

func TestScaleP21SW178CollaborationDocumentDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw178-")
		plain, generation := prefix+"plain", prefix+"generation"
		current, duplicate, collision := prefix+"current", prefix+"duplicate-current", prefix+"collision"
		duplicateFirst, duplicateLast := prefix+"duplicate-first", prefix+"duplicate-last"
		privateDocument, explicitDocument := prefix+"private", prefix+"explicit"
		baseState, firstState := base64.StdEncoding.EncodeToString([]byte("SW178 canonical base")), base64.StdEncoding.EncodeToString([]byte("SW178 first duplicate"))
		private := ""
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw178_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			permission := func(role string) []domain.DocumentPermission {
				return []domain.DocumentPermission{{SubjectType: "workspace", SubjectID: data.Workspace.ID, Role: role}}
			}
			document := func(id, collaboration, state string) domain.Document {
				return domain.Document{
					ID: id, SlugID: id + "-slug", CollaborationID: collaboration, Title: "SW178 " + id,
					Content:      strings.Repeat("Unconsumed collaboration document body. ", 12000),
					ContentState: state, Creator: data.Viewer, TeamIDs: []string{f.public},
					Permissions: permission("editor"),
				}
			}
			first := document(plain, "", baseState)
			restored := document(generation, current, base64.StdEncoding.EncodeToString([]byte("SW178 restored base")))
			restored.PreviousSlugIDs = []string{prefix + "old-slug"}
			earlier := document(collision, prefix+"earlier-generation", baseState)
			later := document(prefix+"later-current", collision, firstState)
			linked := document(privateDocument, prefix+"private-current", baseState)
			linked.Creator, linked.TeamIDs = f.principals[1].user, nil
			linked.InheritedTeamIDs, linked.IssueTeamID = []string{private}, private
			linked.Permissions = permission("viewer")
			explicit := document(explicitDocument, prefix+"explicit-current", baseState)
			explicit.TeamIDs = nil
			explicit.Permissions = []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[2].user.ID, Role: "commenter"}}
			data.Documents = append(data.Documents,
				first, restored,
				document(duplicateFirst, duplicate, firstState), document(duplicateLast, duplicate, baseState),
				earlier, later, linked, explicit,
			)
			return nil
		})
		restore()
		if err != nil || private == "" {
			t.Fatal("seed collaboration document fixture", err)
		}
		at := time.Date(2026, 9, 3, 4, 5, 6, 0, time.UTC)
		for index := range 65 {
			created, err := repo.AppendDocumentCollaborationUpdate(t.Context(), workspace, store.DocumentCollaborationUpdate{
				ID: fmt.Sprintf("collab_%s%03d", prefix, index), DocumentID: plain, ClientID: "sw178-producer",
				Data: []byte{byte(index), 0, 255}, CreatedAt: at.Add(time.Duration(index) * time.Second),
			})
			if err != nil || !created {
				t.Fatal("seed canonical recovery update", index, created, err)
			}
		}
		readKey := &domain.APIKey{Scopes: []string{"read"}}
		writeKey := &domain.APIKey{Scopes: []string{"read", "write"}}
		emptyKey := &domain.APIKey{Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}}
		publicKey := &domain.APIKey{Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
		member, guest := f.principals[2].user, f.principals[3].user
		cases := []scaleP21SW178SocketCase{
			{name: "owner-paged-recovery", workspace: workspace, ref: plain, user: f.data.Viewer},
			{name: "read-key-downgrade", workspace: workspace, ref: plain, user: f.data.Viewer, key: readKey},
			{name: "member-editor-write-key", workspace: workspace, ref: plain, user: member, key: writeKey},
			{name: "guest-workspace-grant", workspace: workspace, ref: plain, user: guest},
			{name: "selected-empty", workspace: workspace, ref: plain, user: member, key: emptyKey},
			{name: "selected-public", workspace: workspace, ref: plain, user: member, key: publicKey},
			{name: "development-key-projection-bypass", workspace: workspace, ref: current, user: guest, key: emptyKey, development: true},
			{name: "current-generation", workspace: workspace, ref: current, user: f.data.Viewer},
			{name: "stale-document-id", workspace: workspace, ref: generation, user: f.data.Viewer},
			{name: "previous-slug-is-not-collaboration-id", workspace: workspace, ref: prefix + "old-slug", user: f.data.Viewer},
			{name: "first-duplicate-current-id", workspace: workspace, ref: duplicate, user: f.data.Viewer},
			{name: "first-stale-id-before-later-current", workspace: workspace, ref: collision, user: f.data.Viewer},
			{name: "private-link-revokes-public-member", workspace: workspace, ref: prefix + "private-current", user: member},
			{name: "explicit-commenter", workspace: workspace, ref: prefix + "explicit-current", user: member},
			{name: "credential-revoked", workspace: workspace, ref: plain, user: f.data.Viewer, revoked: true},
			{name: "credential-invalid", workspace: workspace, ref: plain, user: f.data.Viewer, invalid: true},
			{name: "writer-failure", workspace: workspace, ref: plain, user: f.data.Viewer, writerFailure: true},
			{name: "workspace-missing", workspace: prefix + "missing-space", ref: plain, user: f.data.Viewer},
		}
		syncs, conflicts, revoked, readOnly, editable, more, duplicates, writerFailures := 0, 0, 0, 0, 0, 0, 0, 0
		for _, fixture := range cases {
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			old := scaleP21SW178SocketRun(t, repo, f, fixture)
			fresh := scaleP21SW178SocketRun(t, f.probe, f, fixture)
			if !reflect.DeepEqual(old, fresh) {
				t.Fatalf("socket document %s changed:\n%s", fixture.name, scaleP21M2Diff(old, fresh))
			}
			switch fixture.name {
			case "read-key-downgrade":
				if old.Role != "viewer" || old.RoleError != "" || !old.Joined || old.CanEdit {
					t.Fatal("read-only credential did not downgrade the actual socket", old)
				}
			case "explicit-commenter":
				if old.Role != "commenter" || old.RoleError != "" || !old.Joined || old.CanEdit {
					t.Fatal("explicit commenter did not join read-only", old)
				}
			case "credential-revoked", "credential-invalid":
				if old.Joined || old.JoinError != "" || len(old.Messages) != 1 || !strings.Contains(old.Messages[0], `"type":"document.revoked"`) {
					t.Fatal("revoked credential did not emit the actual join refusal", old)
				}
			}
			for _, raw := range append(append([]string{}, old.Messages...), old.RecheckMessages...) {
				var message struct {
					Type, ContentState string
					Updates            []collaborationSyncUpdate
					ReadOnly, More     bool
				}
				if err := json.Unmarshal([]byte(raw), &message); err != nil {
					t.Fatal(err)
				}
				switch message.Type {
				case "document.sync":
					syncs++
					if message.ReadOnly {
						readOnly++
					} else {
						editable++
					}
					if message.More {
						more++
					}
					if old.AccessDocument == duplicateFirst && message.ContentState == firstState {
						duplicates++
					}
				case "document.conflict":
					conflicts++
				case "document.revoked":
					revoked++
				}
			}
			if fixture.writerFailure {
				if !strings.Contains(old.JoinError, "sw178 socket writer unavailable") {
					t.Fatal("writer failure did not escape the actual join", old)
				}
				writerFailures++
			}
			if fixture.user.ID == f.data.Viewer.ID && fixture.key == nil && !fixture.revoked && !fixture.invalid && !fixture.writerFailure && fixture.workspace == workspace {
				if fixture.ref == duplicate && (old.AccessDocument != duplicateFirst || old.Conflict) {
					t.Fatal("first duplicate collaboration ID did not select the earlier document", old)
				}
				if fixture.ref == collision && (!old.Conflict || old.Joined) {
					t.Fatal("earlier stale document ID did not precede later current collaboration ID", old)
				}
				if fixture.ref == plain {
					updates := 0
					foundState := false
					for _, raw := range old.Messages {
						var message collaborationSyncMessage
						if err := json.Unmarshal([]byte(raw), &message); err != nil {
							t.Fatal(err)
						}
						updates += len(message.Updates)
						foundState = foundState || message.ContentState == baseState
					}
					if updates != 65 || !foundState || len(old.Messages) != 2 {
						t.Fatalf("actual paged recovery missing base or updates: %+v", old)
					}
				}
			}
		}
		if len(cases) == 0 || syncs == 0 || conflicts == 0 || revoked == 0 || readOnly == 0 || editable == 0 || more == 0 || duplicates == 0 || writerFailures == 0 {
			t.Fatalf("missing actual socket branch witnesses rows=%d sync=%d conflict=%d revoked=%d readonly=%d editable=%d more=%d duplicate=%d writerFailure=%d", len(cases), syncs, conflicts, revoked, readOnly, editable, more, duplicates, writerFailures)
		}
		t.Logf("standalone socket document exact rows=%d; role, credential restrictions, generation conflicts, duplicate precedence, paged recovery, access/revocation notices and writer failure", len(cases))
	}
}
