package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type issueCollabOutcome struct {
	Messages     []string
	Joined       bool
	TeamID       string
	CanEdit      bool
	Recheck      bool
	FrameMessage []string
	FrameErr     string
}

// issueCollabRun drives the real join, a recheck and an update frame of one
// issue description on the selected store, draining the socket's output.
func issueCollabRun(t *testing.T, selected *store.SQLiteStore, f scaleP21M2Fixture, user domain.User, issueID, documentID string) issueCollabOutcome {
	t.Helper()
	service := &server{store: selected, realtime: newRealtimeHub()}
	workspace := f.data.Workspace.URLKey
	request := f.literalRequest(user, nil, nil)
	request.Method, request.URL.Path = http.MethodGet, "/api/realtime/socket"
	outputs, closeOutputs := beginMetadataOutputReads(request.Context())
	defer closeOutputs()
	request = request.WithContext(outputs)
	client := service.realtime.addSocket(workspace, "issue-collab", func() {})
	if client == nil {
		t.Fatal("socket admission failed")
	}
	client.userID = user.ID
	client.checkIssue = func(id string) (domain.Issue, bool, bool, error) { return service.socketIssue(request, id) }
	client.checkCredential = func() bool { return true }
	drain := func() []string {
		out := []string{}
		for {
			select {
			case message := <-client.send:
				out = append(out, string(message.data))
				message.release(client)
				if message.sent != nil {
					message.sent <- nil
				}
			default:
				return out
			}
		}
	}
	// join writes through a sent channel that needs a reader.
	stop, done := make(chan struct{}), make(chan []string, 1)
	go func() {
		collected := []string{}
		for {
			select {
			case message := <-client.send:
				collected = append(collected, string(message.data))
				message.release(client)
				if message.sent != nil {
					message.sent <- nil
				}
			case <-stop:
				done <- collected
				return
			}
		}
	}()
	command, _ := json.Marshal(map[string]string{"type": "document.join", "issueId": issueID, "documentId": documentID})
	if err := service.handleCollaborationCommand(request, client, command); err != nil {
		t.Fatal(err)
	}
	close(stop)
	var result issueCollabOutcome
	// A frame queued without a delivery wait (document.conflict) may still be
	// in the channel when the collector sees stop: select picks among ready
	// cases at random.
	result.Messages = append(<-done, drain()...)
	service.realtime.mu.Lock()
	_, result.Joined = client.documents[documentID]
	if access := client.issueAccess[documentID]; access != nil {
		result.TeamID, result.CanEdit = access.teamID, access.canEdit
	}
	service.realtime.mu.Unlock()
	if result.Joined {
		result.Recheck, _ = service.recheckIssueDocument(client, documentID, issueID)
		frame := encodeCollaborationFrame(collaborationAwarenessFrame, documentID, "", []byte{1, 2, 3})
		if err := service.handleCollaborationFrame(request, client, frame); err != nil {
			result.FrameErr = err.Error()
		}
		result.FrameMessage = drain()
	}
	service.realtime.removeSocket(client)
	return result
}

func TestScaleP21IssueCollaborationJoinDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		admin := f.principals[1]
		client, _ := scaleP03Join(t, f.scaleP03PlatformFixture, "collab-admin", idgen.New("collab-user-")+"@example.test", "admin", f.public)
		plain := authRequest[domain.Issue](t, client, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Plain description", "teamId": f.public}, workspace, http.StatusCreated)
		custom := authRequest[domain.Issue](t, client, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Collaborative description", "teamId": f.public}, workspace, http.StatusCreated)
		generation := idgen.New("document_content_generation_")
		state := base64.StdEncoding.EncodeToString([]byte("collaborative description base"))
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "issue_collab.seed", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Issues {
				if data.Issues[index].ID == custom.ID {
					data.Issues[index].DocumentContent = &domain.DocumentContent{ID: generation, Version: 3, ContentState: state, Content: strings.Repeat("body ", 100)}
				}
			}
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		_ = admin
		cases := []struct {
			name, issue, document string
			wantType              string
		}{
			{"custom-generation", custom.ID, generation, "document.sync"},
			{"custom-stale-legacy-id", custom.ID, "document_content_" + custom.ID, "document.conflict"},
			{"plain-default-id", plain.ID, "document_content_" + plain.ID, "document.sync"},
			{"plain-wrong-id", plain.ID, generation, "document.conflict"},
		}
		user := f.data.Viewer
		for _, fixture := range cases {
			for _, reloaded := range []bool{false, true} {
				if reloaded {
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
				}
				old := issueCollabRun(t, repo, f, user, fixture.issue, fixture.document)
				fresh := issueCollabRun(t, f.probe, f, user, fixture.issue, fixture.document)
				if !reflect.DeepEqual(old, fresh) {
					t.Fatalf("%s reloaded=%v changed:\n%s", fixture.name, reloaded, scaleP21M2Diff(old, fresh))
				}
				if len(old.Messages) == 0 || !strings.Contains(old.Messages[0], `"type":"`+fixture.wantType+`"`) {
					t.Fatalf("%s: unexpected first frame %v", fixture.name, old.Messages)
				}
				if fixture.wantType == "document.sync" && (!old.Joined || old.TeamID == "" || !old.Recheck) {
					t.Fatalf("%s: join state %+v", fixture.name, old)
				}
			}
		}
	}
}
