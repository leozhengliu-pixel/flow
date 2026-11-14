package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW25ErasureIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW25 foreign erasure", idgen.New("sw25-erasure-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "projects")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, test := range []struct{ key, id string }{
				{workspace, f.data.Workspace.ID},
				{foreign.Workspace.URLKey, foreign.Workspace.ID},
				{workspace + "-missing", ""},
				{strings.ToUpper(workspace), ""},
				{workspace + " ", ""},
				{workspace + "\x00", ""},
			} {
				request := f.request(user, key, nil)
				var results []any
				for _, source := range []*store.SQLiteStore{repo, probe} {
					id, ok := (&server{store: source}).eraseMemberWorkspaceID(request.Context(), test.key)
					if id != test.id || ok != (test.id != "") {
						t.Fatalf("trusted identity %q=%q/%t want=%q", test.key, id, ok, test.id)
					}
					results = append(results, []any{id, ok})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("erasure success inputs differ")
				}
			}
			for _, source := range []*store.SQLiteStore{repo, probe} {
				previous, found := source.WorkspaceMetadata("")
				id, ok := (&server{store: source}).eraseMemberWorkspaceID(f.request(user, key, nil).Context(), "")
				if id != previous.Workspace.ID || ok != found {
					t.Fatal("default installed workspace selection changed", id, ok, previous.Workspace.ID, found)
				}
			}
			// These refusal paths remain exact. The identity adapter is trusted;
			// existing route authorization and erasure checks stay independent.
			for _, target := range []string{user.ID, idgen.New("sw25-missing-")} {
				var responses []any
				for _, source := range []*store.SQLiteStore{repo, probe} {
					request := f.request(user, key, map[string]string{"confirmation": "wrong"})
					request.SetPathValue("userId", target)
					response := httptest.NewRecorder()
					(&server{store: source}).eraseMember(response, request)
					responses = append(responses, []any{response.Code, response.Header(), response.Body.String()})
				}
				if !reflect.DeepEqual(responses[0], responses[1]) {
					t.Fatal("erasure refusal differs", responses)
				}
			}
		})
		member := f.principals[2].user
		queued := f.request(member, nil, nil)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				id, ok := (&server{store: source}).eraseMemberWorkspaceID(queued.Context(), workspace)
				if !ok || id != f.data.Workspace.ID {
					t.Fatal(phase, "identity lookup invented membership authorization", id, ok)
				}
			}
		}
		if err := repo.RemoveMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		var formerResponses []any
		for _, source := range []*store.SQLiteStore{repo, probe} {
			request := f.request(f.data.Viewer, nil, map[string]string{"confirmation": "wrong"})
			request.SetPathValue("userId", member.ID)
			response := httptest.NewRecorder()
			(&server{store: source}).eraseMember(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatal("former-member map was unavailable through its use", response.Code, response.Body.String())
			}
			formerResponses = append(formerResponses, []any{response.Code, response.Header(), response.Body.String()})
		}
		if !reflect.DeepEqual(formerResponses[0], formerResponses[1]) {
			t.Fatal("former-member erasure refusal differs", formerResponses)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		for _, source := range []*store.SQLiteStore{repo, probe} {
			if id, ok := (&server{store: source}).eraseMemberWorkspaceID(cancelled, workspace); !ok || id != f.data.Workspace.ID {
				t.Fatal("installed identity read introduced cancellation or membership SQL", id, ok)
			}
			if id, ok := (&server{store: source}).eraseMemberWorkspaceID(t.Context(), workspace); !ok || id != f.data.Workspace.ID {
				t.Fatal("identity lookup changed after cancellation", id, ok)
			}
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "projects") || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("identity read changed unrelated bodies or decoded full metadata")
		}
	}
}

func TestScaleP21SW25ErasureLegacySuccessAndRemainingWriter(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		target := f.principals[2].user
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		request := func() *http.Request {
			request := f.request(f.data.Viewer, nil, map[string]string{"confirmation": target.Email})
			request.SetPathValue("userId", target.ID)
			return request
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "users")
		bounded := httptest.NewRecorder()
		(&server{store: probe}).eraseMember(bounded, request())
		if bounded.Code != http.StatusInternalServerError || !strings.Contains(bounded.Body.String(), "no migrated point writer") {
			t.Fatal("independent downstream erasure blocker changed", bounded.Code, bounded.Body.String())
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "users") {
			t.Fatal("unported bounded erasure changed users before refusing")
		}
		legacy := httptest.NewRecorder()
		(&server{store: repo}).eraseMember(legacy, request())
		if legacy.Code != http.StatusOK {
			t.Fatal("off-mode erasure success regressed", legacy.Code, legacy.Body.String())
		}
		var result map[string]string
		if err := json.Unmarshal(legacy.Body.Bytes(), &result); err != nil || result["userId"] != target.ID || result["scope"] != "account" {
			t.Fatal("off-mode erasure result", result, err)
		}
		if erased, err := repo.ErasedFromWorkspace(t.Context(), f.data.Workspace.ID, target.ID); err != nil || !erased {
			t.Fatal("off-mode erasure did not retain its effect", erased, err)
		}
	}
}

func TestScaleP21SW25ErasureIdentityBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		run := func(source *store.SQLiteStore) {
			t.Helper()
			id, ok := (&server{store: source}).eraseMemberWorkspaceID(t.Context(), f.data.Workspace.URLKey)
			if !ok || id != f.data.Workspace.ID {
				t.Fatal("erasure workspace identity", id, ok)
			}
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for sample := range 5 {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				run(source)
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				for range 3 {
					run(source)
				}
				runtime.ReadMemStats(&after)
				t.Logf("sample=%d bounded=%t bytes/op=%d allocs/op=%d", sample+1, source.BoundedMetadataEnabled(), (after.TotalAlloc-before.TotalAlloc)/3, (after.Mallocs-before.Mallocs)/3)
			}
		}
	}
}
