package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW19ChatPolicyBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		manageable := []string{}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw19.chat_baseline", "", nil, func(data *domain.Bootstrap) error {
			for i := range 75 {
				id := fmt.Sprintf("sw19-chat-baseline-%03d", i)
				data.Teams = append(data.Teams, domain.Team{ID: id, Name: strings.Repeat("name ", 800)})
				data.TeamSettings[id] = domain.TeamSettings{TeamID: id}
				manageable = append(manageable, id)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		service := &server{store: repo}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for range 5 {
			if got := service.chatBindableTeamIDs(workspace, manageable); len(got) != len(manageable) {
				t.Fatal("baseline dropped a bindable team")
			}
		}
		runtime.ReadMemStats(&after)
		t.Logf("legacy chat policy n=5 bytes/op=%d allocs/op=%d ns/op=%d", (after.TotalAlloc-before.TotalAlloc)/5, (after.Mallocs-before.Mallocs)/5, time.Since(start).Nanoseconds()/5)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&before)
		start = time.Now()
		for range 5 {
			ids, read, err := (&server{store: probe}).chatBindableTeamIDsRead(t.Context(), workspace, manageable)
			if err != nil || len(ids) != len(manageable) {
				t.Fatal("native policy dropped a bindable team", err)
			}
			read.Close()
		}
		runtime.ReadMemStats(&after)
		t.Logf("native chat policy n=5 bytes/op=%d allocs/op=%d ns/op=%d", (after.TotalAlloc-before.TotalAlloc)/5, (after.Mallocs-before.Mallocs)/5, time.Since(start).Nanoseconds()/5)
	}
}

func TestScaleP21SW19ChatBindablePolicyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		private := "sw19-chat-private"
		excluded := []string{"sw19-chat-archived", "sw19-chat-retired", "sw19-chat-archived-child", "sw19-chat-missing-parent", "sw19-chat-cycle-a", "sw19-chat-cycle-b"}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw19.chat_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range []domain.Team{
				{ID: private, Private: true},
				{ID: "sw19-chat-access-private"},
				{ID: "sw19-chat-access-restricted"},
				{ID: "sw19-chat-members"},
				{ID: "sw19-chat-owners"},
				{ID: excluded[0], ArchivedAt: &stamp},
				{ID: excluded[1], RetiredAt: &stamp},
				{ID: excluded[2]},
				{ID: excluded[3]},
				{ID: excluded[4]},
				{ID: excluded[5]},
			} {
				data.Teams = append(data.Teams, team)
			}
			data.TeamSettings["sw19-chat-access-private"] = domain.TeamSettings{Access: " PRIVATE "}
			data.TeamSettings["sw19-chat-access-restricted"] = domain.TeamSettings{Access: " Restricted "}
			data.TeamSettings["sw19-chat-members"] = domain.TeamSettings{MembershipRestriction: " MEMBERS "}
			data.TeamSettings["sw19-chat-owners"] = domain.TeamSettings{MembershipRestriction: " Owners "}
			data.TeamSettings[excluded[2]] = domain.TeamSettings{ParentTeamID: excluded[0]}
			data.TeamSettings[excluded[3]] = domain.TeamSettings{ParentTeamID: "sw19-nonexistent-parent"}
			data.TeamSettings[excluded[4]] = domain.TeamSettings{ParentTeamID: excluded[5]}
			data.TeamSettings[excluded[5]] = domain.TeamSettings{ParentTeamID: excluded[4]}
			parent := private
			for i := range 75 {
				id := fmt.Sprintf("sw19-chat-chain-%03d", i)
				data.Teams = append(data.Teams, domain.Team{ID: id, Name: strings.Repeat("unused name ", 400)})
				data.TeamSettings[id] = domain.TeamSettings{ParentTeamID: parent, Description: strings.Repeat("unused policy ", 250)}
				parent = id
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		f.probe = probe
		for _, owner := range []struct{ team, user string }{
			{f.public, f.data.Viewer.ID}, {f.public, f.principals[2].user.ID},
			{private, f.data.Viewer.ID}, {private, f.principals[4].user.ID},
		} {
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, owner.team, owner.user, "owner", true); err != nil {
				t.Fatal(err)
			}
		}
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("sw19-chat-"), AppID: "sw19-app", TenantKey: "sw19-tenant", Connected: true}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		for _, team := range []string{f.public, private} {
			if err := repo.PutChatBinding(t.Context(), workspace, domain.ChatBinding{ID: idgen.New("sw19-binding-"), ConnectionID: record.ConnectionID, TeamID: team, ChatID: team}); err != nil {
				t.Fatal(err)
			}
		}
		legacy, native := &server{store: repo}, &server{store: probe}
		data, _ := repo.WorkspaceMetadata(workspace)
		all := []string{"sw19-missing"}
		for _, team := range data.Teams {
			all = append(all, team.ID)
		}
		slices.Reverse(all)
		all = append(all, all...)
		want := legacy.chatBindableTeamIDs(workspace, all)
		got, read, err := native.chatBindableTeamIDsRead(t.Context(), workspace, all)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("full policy/state/inheritance/order differential", got, want, err)
		}
		read.Close()
		for _, id := range excluded {
			if slices.Contains(got, id) {
				t.Fatalf("inactive/cyclic/missing-parent team became bindable: %s", id)
			}
		}
		for _, id := range []string{private, "sw19-chat-access-private", "sw19-chat-access-restricted", "sw19-chat-members", "sw19-chat-owners", "sw19-chat-chain-074"} {
			if !slices.Contains(got, id) {
				t.Fatalf("authorized private/inherited team was dropped: %s", id)
			}
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			view := data
			view.Viewer = user
			role, _, err := repo.WorkspaceRole(t.Context(), view.Workspace.ID, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			view.ViewerRole = role
			var responses []*httptest.ResponseRecorder
			for _, service := range []*server{legacy, native} {
				request := f.request(user, key, nil)
				request.SetPathValue("provider", "lark")
				response := httptest.NewRecorder()
				service.writeChatSettings(response, request, view)
				responses = append(responses, response)
			}
			if responses[0].Code != http.StatusOK || responses[1].Code != responses[0].Code || responses[1].Body.String() != responses[0].Body.String() {
				t.Fatal("actual chat settings encoder differs", responses[0].Code, responses[1].Code)
			}
		})
		// Queued/replayed owner requests use fresh SQL manageable-team
		// decisions after revocation, rather than retaining a prior ID set.
		member := f.principals[2].user
		view := data
		view.Viewer, view.ViewerRole = member, "member"
		queued := f.request(member, nil, nil)
		queued.SetPathValue("provider", "lark")
		if !slices.Contains(legacy.chatManageableTeamIDs(queued, view), f.public) {
			t.Fatal("revocation fixture does not own public team")
		}
		if err := repo.SetTeamMembership(t.Context(), view.Workspace.ID, f.public, member.ID, "member", false); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			for _, service := range []*server{legacy, native} {
				response := httptest.NewRecorder()
				service.writeChatSettings(response, queued, view)
				var result struct {
					IDs []string `json:"bindableTeamIds"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || slices.Contains(result.IDs, f.public) {
					t.Fatal("revoked owner retained bindable team", response.Code)
				}
			}
		}
		foreign := scaleP03Fixture(t, repo)
		for _, service := range []*server{legacy, native} {
			request := f.request(foreign.data.Viewer, nil, nil)
			request.SetPathValue("provider", "lark")
			response := httptest.NewRecorder()
			service.chatSettings(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("foreign workspace viewer=%d", response.Code)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, read, err := native.chatBindableTeamIDsRead(ctx, workspace, all); read != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("native policy cancellation", err)
		}
		if native.chatBindableTeamIDs(workspace+"-missing", all) == nil || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("missing workspace empty shape or native full decode changed")
		}
	}
}

type scaleP21SW19ChatWriter struct {
	*httptest.ResponseRecorder
	onWrite func()
}

func (w *scaleP21SW19ChatWriter) Write(body []byte) (int, error) {
	if w.onWrite != nil {
		action := w.onWrite
		w.onWrite = nil
		action()
	}
	return w.ResponseRecorder.Write(body)
}

func TestScaleP21SW19ChatPolicyHTTPAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("sw19-http-"), AppID: "sw19-http", Connected: true}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		held := []*store.ChatTeamPolicyRead{}
		defer func() {
			for _, read := range held {
				read.Close()
			}
		}()
		for {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			read, found, err := probe.BeginChatTeamPolicyRead(ctx, workspace)
			cancel()
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			if err != nil || !found || len(held) >= 128 {
				t.Fatal("fill native work allowance", found, err)
			}
			held = append(held, read)
		}
		held[len(held)-1].Close()
		held = held[:len(held)-1]
		response := &scaleP21SW19ChatWriter{ResponseRecorder: httptest.NewRecorder()}
		encoded := false
		response.onWrite = func() {
			encoded = true
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			read, _, err := probe.BeginChatTeamPolicyRead(ctx, workspace)
			if read != nil {
				read.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("actual HTTP encoding did not retain policy admission: %v", err)
			}
		}
		request := httptest.NewRequest(http.MethodGet, "/api/integrations/lark/chat", nil)
		request.Header.Set("X-Workspace-Key", workspace)
		request.SetPathValue("provider", "lark")
		(&server{store: probe, authDisabled: true}).chatSettings(response, request)
		if response.Code != http.StatusOK || !encoded {
			t.Fatal("actual HTTP endpoint was not encoded", response.Code, response.Body.String())
		}
		read, found, err := probe.BeginChatTeamPolicyRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("HTTP encoder leaked admission", found, err)
		}
		read.Close()
		for _, heldRead := range held {
			heldRead.Close()
		}
		held = nil
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.HIPAACompliance = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		hipaa, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, service := range []*server{{store: repo, authDisabled: true}, {store: hipaa, authDisabled: true}} {
			response := httptest.NewRecorder()
			service.chatSettings(response, request)
			var result struct {
				IDs []string `json:"bindableTeamIds"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.IDs) != 0 {
				t.Fatal("HIPAA workspace exposed bindable teams", response.Code)
			}
		}
	}
}

func TestScaleP21SW19ChatPolicyTimestampDecisionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	dst, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	zones := []*time.Location{time.UTC, time.Local, dst, time.FixedZone(strings.Repeat("retired-source-zone", 100000), -18000)}
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		ids := []string{}
		times := map[string]*time.Time{}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw19.chat_timestamp_presence", "", nil, func(data *domain.Bootstrap) error {
			for index, zone := range zones {
				archived, retired := time.Date(2026, 7, 1, 12, 0, 0, 0, zone), time.Date(2026, 1, 1, 12, 0, 0, 0, zone)
				base := fmt.Sprintf("sw19-zone-%d", index)
				for _, team := range []domain.Team{
					{ID: base + "-active", Private: true},
					{ID: base + "-archived", ArchivedAt: &archived},
					{ID: base + "-retired", RetiredAt: &retired},
					{ID: base + "-archived-child"},
					{ID: base + "-retired-child"},
					{ID: base + "-active-child"},
				} {
					data.Teams = append(data.Teams, team)
					ids = append(ids, team.ID)
				}
				data.TeamSettings[base+"-archived-child"] = domain.TeamSettings{ParentTeamID: base + "-archived"}
				data.TeamSettings[base+"-retired-child"] = domain.TeamSettings{ParentTeamID: base + "-retired"}
				data.TeamSettings[base+"-active-child"] = domain.TeamSettings{ParentTeamID: base + "-active"}
				times[base+"-archived"], times[base+"-retired"] = &archived, &retired
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		legacy, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("missing exact installed source")
		}
		// Read headers from this same installed source, before any reload or
		// canonical normalization, to compare the actual trusted predicates.
		read, found, err := repo.BeginChatTeamPolicyRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("exact-source header read", found, err)
		}
		defer read.Close()
		headers := read.Metadata()
		for _, id := range ids {
			active, private := chatTeamState(legacy, id)
			gotActive, gotPrivate := chatTeamState(headers, id)
			if active != gotActive || private != gotPrivate {
				t.Fatalf("timestamp presence changed exact-source state for %s", id)
			}
			for _, authorized := range []bool{false, true} {
				binding := domain.ChatBinding{TeamID: id, Authorization: &domain.ChatBindingAuthorization{PrivateTeam: authorized}}
				if chatBindingScopePermitted(legacy, binding) != chatBindingScopePermitted(headers, binding) {
					t.Fatalf("timestamp presence changed exact-source binding permission for %s", id)
				}
			}
		}
		for _, team := range legacy.Teams {
			if expected, ok := times[team.ID]; ok {
				value := team.ArchivedAt
				if value == nil {
					value = team.RetiredAt
				}
				if value == nil || *value != *expected || value.Location() != expected.Location() {
					t.Fatal("legacy fixture timestamp was normalized by header read")
				}
			}
		}
		for _, team := range headers.Teams {
			for _, value := range []*time.Time{team.ArchivedAt, team.RetiredAt} {
				if value != nil && (!value.IsZero() || value.Location() != time.UTC) {
					t.Fatal("bindability header exposed source time values")
				}
			}
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := (&server{store: repo}).chatBindableTeamIDs(workspace, ids)
		got, retained, err := (&server{store: probe}).chatBindableTeamIDsRead(t.Context(), workspace, ids)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("timestamp headers changed effective bindable IDs", got, want, err)
		}
		retained.Close()
	}
}
