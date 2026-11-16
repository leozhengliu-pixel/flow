package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW185MemberImage(t *testing.T, repo *store.SQLiteStore, workspace string) string {
	t.Helper()
	var members []domain.WorkspaceMember
	var teamMembers []domain.TeamMember
	if !repo.BoundedMetadataEnabled() {
		data, found := repo.WorkspaceMetadataForRead(t.Context(), workspace)
		if !found {
			t.Fatal("member image workspace missing")
		}
		members, teamMembers = data.Members, data.TeamMembers
	} else {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("member image", err)
		}
		defer read.Close()
		for _, field := range []struct {
			name   string
			target any
		}{{"members", &members}, {"teamMembers", &teamMembers}} {
			var raw strings.Builder
			if err := read.StreamFieldJSON(t.Context(), field.name, &raw); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw.String()), field.target); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, err := json.Marshal([]any{members, teamMembers})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestScaleP21SW185TriageTeamMoveKeepsStaleMemberSnapshots(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamMoveSetup(t)
	workspace := f.data.Workspace.URLKey
	seed := f.prepare(scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"})
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		seed(t)
		restore := store.ForceFullMutationsForTesting(true)
		defer restore()
		if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw185_members", "", nil, func(data *domain.Bootstrap) error {
			stale := data.Viewer
			stale.Name = "Stored member snapshot"
			data.Members = []domain.WorkspaceMember{{User: stale, Role: "guest", Status: "suspended", JoinedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}}
			data.TeamMembers = []domain.TeamMember{{TeamID: "tm-parent", UserID: stale.ID, Role: "member", Managed: true, ManagedSource: "stored-snapshot", Origin: "sync"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var expected string
	outcomes := f.differential(t, "stale member snapshots", func(service *server) *httptest.ResponseRecorder {
		before := scaleP21SW185MemberImage(t, service.store, workspace)
		if !strings.Contains(before, "Stored member snapshot") {
			t.Fatal("fixture did not retain stale member snapshot")
		}
		if expected == "" {
			expected = before
		} else if before != expected {
			t.Fatal("reset changed stored member snapshots")
		}
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		results, err := service.generateTriageIntelligenceForIssues(r.Context(), workspace, []string{f.triage.ID})
		if err != nil {
			t.Fatal(err)
		}
		after := scaleP21SW185MemberImage(t, service.store, workspace)
		if after != before {
			t.Fatalf("bounded=%t replaced stored member snapshots: %s", service.store.BoundedMetadataEnabled(), scaleP21M2Diff(before, after))
		}
		w := httptest.NewRecorder()
		writeJSON(w, http.StatusOK, results)
		return w
	})
	for _, outcome := range outcomes {
		if issue := scaleP21TeamMoveIssue(t, outcome, f.triage.ID); issue.Team.ID != "tm-parent" {
			t.Fatalf("issue did not move: %s", issue.Team.ID)
		}
	}
}
