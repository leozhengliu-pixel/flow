package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21IssueAuthorizationFixture struct {
	scaleP09Fixture
	guest                           domain.User
	public, private, child, sibling string
	comment, foreignIssue           string
}

func scaleP21IssueAuthorizationSeed(t *testing.T) scaleP21IssueAuthorizationFixture {
	t.Helper()
	f := scaleP21IssueAuthorizationFixture{scaleP09Fixture: scaleP09Database(t)}
	workspace := f.data.Workspace.URLKey
	prefix := idgen.New("issue-auth-")
	f.public, f.private, f.child, f.sibling, f.comment = prefix+"public", prefix+"private", prefix+"child", prefix+"sibling", prefix+"comment"
	at := time.Date(2026, 11, 14, 1, 2, 3, 400, time.UTC)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.issue_authorization_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams,
			domain.Team{ID: f.public, Key: "AUTH", Name: strings.Repeat("ignored team body ", 1000)},
			domain.Team{ID: f.private, Key: "SECRET", Private: true},
			domain.Team{ID: prefix + "archived", Key: "ARCH", ArchivedAt: &at},
			domain.Team{ID: prefix + "retired", Key: "RET", RetiredAt: &at},
		)
		data.TeamSettings[f.public] = domain.TeamSettings{TeamID: f.public, Access: "public"}
		data.TeamSettings[f.private] = domain.TeamSettings{TeamID: f.private, Access: "private", MembershipRestriction: "invite"}
		data.TeamSettings[prefix+"retired"] = domain.TeamSettings{TeamID: prefix + "retired", ParentTeamID: f.public, Access: "public"}
		data.WorkspaceSettings.HIPAACompliance, data.WorkspaceSettings.PreventGuestAgents = true, true
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["ai-agent"], data.WorkspaceSettings.FeatureFlags["ai"] = false, true
		data.WorkspaceSettings.FeatureFlags["unrelated"] = true
		if data.UserSettings == nil {
			data.UserSettings = map[string]domain.UserSettings{}
		}
		data.UserSettings[data.Viewer.ID] = domain.UserSettings{UserID: data.Viewer.ID, AgentEnabled: false, SuggestedActionsOnComments: new(false)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	guest, token, err := f.Register(t.Context(), "Issue authorization guest", idgen.New("issue-auth-")+"@example.test", "issue-auth-password")
	if err != nil {
		t.Fatal(err)
	}
	f.guest = guest
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = f.ErasePersonFromWorkspace(ctx, workspace, guest.ID)
		_ = f.EraseAccount(ctx, guest.ID)
	})
	if err := f.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	invitation, err := f.Invite(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID, guest.Email, "guest", []string{f.public})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AcceptInvitation(t.Context(), invitation.Token, guest.ID); err != nil {
		t.Fatal(err)
	}
	parentID, grandID := prefix+"parent", prefix+"grand"
	oldParent := strings.ToUpper(prefix) + "OLD-2"
	base := domain.Issue{Version: 1, Team: domain.Team{ID: f.public, Key: "stale", Private: true}, Creator: f.data.Viewer,
		State: domain.WorkflowState{ID: "auth-state", TeamID: f.public, Type: "unstarted"}, CreatedAt: at, UpdatedAt: at}
	child := base
	child.ID, child.Identifier, child.Number, child.SortOrder, child.ParentID = f.child, "AUTH-1", 1, 2, new(strings.ToLower(oldParent))
	child.Permissions = []domain.IssuePermission{{ID: prefix + "grant", SubjectType: "user", SubjectID: guest.ID, Role: "editor"}}
	child.Relations = []domain.IssueRelation{{ID: prefix + "relation", IssueID: f.child, RelatedIssueID: f.sibling, Type: "blocks"}}
	sibling := base
	sibling.ID, sibling.Identifier, sibling.Number, sibling.SortOrder, sibling.ParentID = f.sibling, "AUTH-2", 2, 1, &parentID
	sibling.ArchivedAt = &at
	parent := base
	parent.ID, parent.Identifier, parent.Number, parent.SortOrder, parent.ParentID = parentID, "PARENT-2", 20, 3, &grandID
	parent.PreviousIdentifiers = []string{oldParent}
	parent.Team = domain.Team{ID: f.private}
	grand := parent
	grand.ID, grand.Identifier, grand.Number, grand.SortOrder, grand.ParentID, grand.PreviousIdentifiers = grandID, "GRAND-3", 30, 4, &f.child, nil
	hidden := parent
	hidden.ID, hidden.Identifier, hidden.ParentID, hidden.PreviousIdentifiers = prefix+"hidden", "HIDDEN-4", nil, nil
	if err := f.ImportIssues(t.Context(), workspace, []domain.Issue{child, sibling, parent, grand, hidden}); err != nil {
		t.Fatal(err)
	}
	comment, _ := json.Marshal(domain.Comment{ID: f.comment, Body: "ignored body", User: domain.User{ID: guest.ID}})
	comment = append(comment[:len(comment)-1], []byte(`,"ID":"decoded-comment","USER":{"ID":"`+guest.ID+`"}}`)...)
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,'comment',?,?,?,?)`,
		workspace, f.child, f.comment, at.Format(time.RFC3339Nano), comment); err != nil {
		t.Fatal(err)
	}
	// A supported SQL producer changes a reference row while the installed
	// native preimage remains warm. IssueRecord refreshes this canonical key.
	var teamRaw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, workspace, f.public).Scan(&teamRaw); err != nil {
		t.Fatal(err)
	}
	teamRaw = append(teamRaw[:len(teamRaw)-1], []byte(`,"KEY":"CANONICAL-AUTH"}`)...)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teams' AND record_key=?`, teamRaw, workspace, f.public); err != nil {
		t.Fatal(err)
	}
	foreign, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Foreign authorization fixture", idgen.New("issue-auth-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey) })
	f.foreignIssue = prefix + "foreign-issue"
	hidden.ID, hidden.Identifier, hidden.Team, hidden.ParentID = f.foreignIssue, "FOREIGN-1", foreign.Teams[0], nil
	if err := f.ImportIssues(t.Context(), foreign.Workspace.URLKey, []domain.Issue{hidden}); err != nil {
		t.Fatal(err)
	}
	return f
}

func scaleP21IssueAuthorizationHeader(issue domain.Issue) domain.Issue {
	out := domain.Issue{ID: issue.ID, Identifier: issue.Identifier, PreviousIdentifiers: issue.PreviousIdentifiers, Number: issue.Number,
		Team: domain.Team{ID: issue.Team.ID, Key: issue.Team.Key, Private: issue.Team.Private}, Creator: domain.User{ID: issue.Creator.ID},
		ParentID: issue.ParentID, ArchivedAt: issue.ArchivedAt}
	if issue.Permissions != nil {
		out.Permissions = make([]domain.IssuePermission, len(issue.Permissions))
		for i, permission := range issue.Permissions {
			out.Permissions[i] = domain.IssuePermission{ID: permission.ID, SubjectType: permission.SubjectType, SubjectID: permission.SubjectID, Role: permission.Role}
		}
	}
	if issue.Relations != nil {
		out.Relations = make([]domain.IssueRelation, len(issue.Relations))
		for i, relation := range issue.Relations {
			out.Relations[i] = domain.IssueRelation{ID: relation.ID, IssueID: relation.IssueID, RelatedIssueID: relation.RelatedIssueID}
		}
	}
	return out
}

func scaleP21IssueAuthorizationHeaders(issues []domain.Issue) []domain.Issue {
	result := make([]domain.Issue, len(issues))
	for i, issue := range issues {
		result[i] = scaleP21IssueAuthorizationHeader(issue)
	}
	return result
}

func scaleP21IssueAuthorizationPolicy(data domain.Bootstrap, key *domain.APIKey, dev bool) any {
	type team struct {
		ID, Key, ExternalSource    string
		Private, Archived, Retired bool
	}
	type settings struct{ Parent, Access, Restriction string }
	type membership struct{ Team, User, Role string }
	type preference struct {
		Saved, AgentEnabled bool
		UserID              string
		Suggested           *bool
	}
	result := struct {
		WorkspaceID, WorkspaceKey, Role string
		Viewer                          domain.User
		Teams                           []team
		Settings                        map[string]settings
		Members                         []membership
		ViewerTeams                     []string
		HIPAA, PreventGuestAgents       bool
		Flags                           map[string]bool
		Preference                      preference
	}{WorkspaceID: data.Workspace.ID, WorkspaceKey: data.Workspace.URLKey, Viewer: data.Viewer, Role: data.ViewerRole,
		Teams: []team{}, Settings: map[string]settings{}, Members: []membership{}, ViewerTeams: data.ViewerTeamIDs,
		HIPAA: data.WorkspaceSettings.HIPAACompliance, PreventGuestAgents: data.WorkspaceSettings.PreventGuestAgents, Flags: map[string]bool{}}
	for _, flag := range []string{"ai-agent", "ai"} {
		if enabled, found := data.WorkspaceSettings.FeatureFlags[flag]; found {
			result.Flags[flag] = enabled
		}
	}
	if item, found := data.UserSettings[data.Viewer.ID]; found {
		result.Preference = preference{true, item.AgentEnabled, item.UserID, item.SuggestedActionsOnComments}
	}
	visible := map[string]bool{}
	for _, item := range data.Teams {
		if !dev && key != nil && key.TeamRestriction == "selected" && !slices.Contains(key.TeamIDs, item.ID) {
			continue
		}
		visible[item.ID] = true
		result.Teams = append(result.Teams, team{item.ID, item.Key, item.ExternalSource, item.Private, item.ArchivedAt != nil, item.RetiredAt != nil})
	}
	for id, item := range data.TeamSettings {
		if visible[id] {
			result.Settings[id] = settings{item.ParentTeamID, item.Access, item.MembershipRestriction}
		}
	}
	for _, item := range data.TeamMembers {
		if item.UserID == data.Viewer.ID && visible[item.TeamID] {
			result.Members = append(result.Members, membership{item.TeamID, item.UserID, item.Role})
		}
	}
	if dev {
		result.Members, result.ViewerTeams = []membership{}, nil
	}
	return result
}

// This follows the literal child-write alias using full legacy storage reads.
func scaleP21IssueAuthorizationLegacy(t *testing.T, f scaleP09Fixture, userID string, request IssueWriteAuthorizationRequest) (domain.Bootstrap, error) {
	t.Helper()
	query := request.Query
	var data domain.Bootstrap
	if userID == "" {
		var found bool
		data, found = f.WorkspaceMetadata(query.Workspace)
		if !found {
			return data, ErrAuthForbidden
		}
		data.ViewerRole = "admin"
		query.Access, query.AllowedTeamIDs = nil, nil
		if archived := ArchivedTeamIDs(data); len(archived) != 0 {
			query.Access = &IssueRecordAccess{Admin: true, ArchivedTeamIDs: archived}
		}
	} else {
		var err error
		data, err = f.PagedWorkspaceMetadata(t.Context(), query.Workspace, userID)
		if err != nil {
			return data, err
		}
	}
	query.Filter = IssueFilter{Field: "id", Values: request.IDs}
	query.Archived, query.Limit, query.Cursor = "all", 500, ""
	query.GroupValue, query.GroupValues = nil, nil
	query.TeamIDs, query.ProjectIDs, query.StateIDs, query.Text = nil, nil, nil, ""
	page, err := f.QueryIssueRecords(t.Context(), query)
	if err != nil {
		return data, err
	}
	data.Issues = page.Items
	if page.HasMore {
		query.Cursor = page.NextCursor
		next, err := f.QueryIssueRecords(t.Context(), query)
		if err != nil {
			return data, err
		}
		data.Issues = append(data.Issues, next.Items...)
	}
	seen := map[string]bool{}
	for _, issue := range page.Items {
		seen[issue.ID] = true
		for parent := issue.ParentID; parent != nil && *parent != "" && !seen[*parent] && len(seen) < 64; {
			ancestor, err := f.IssueRecord(t.Context(), query.Workspace, *parent)
			if err != nil {
				break
			}
			seen[ancestor.ID] = true
			data.Issues = append(data.Issues, ancestor)
			parent = ancestor.ParentID
		}
	}
	data.Comments = map[string][]domain.Comment{}
	if request.CommentID != "" {
		comment, err := f.ResourceComment(t.Context(), query.Workspace, request.CommentIssueID, request.CommentID)
		if errors.Is(err, sql.ErrNoRows) {
			return data, nil
		}
		if err != nil {
			return data, err
		}
		data.Comments[request.CommentIssueID] = []domain.Comment{{ID: comment.ID, User: domain.User{ID: comment.User.ID}}}
	}
	return data, nil
}

func scaleP21IssueAuthorizationView(t *testing.T, f scaleP09Fixture, reload bool) *SQLiteStore {
	t.Helper()
	if reload {
		f.mu.Lock()
		f.forgetCopyRevisionLocked(f.data.Workspace.URLKey)
		f.mu.Unlock()
		if err := f.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	return view
}

func TestScaleP21IssueAuthorizationLegacyHeaders(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	workspace := f.data.Workspace.URLKey
	_, access, err := f.IssueQueryAccess(t.Context(), workspace, f.data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.AuthorizedIssueRecordID(t.Context(), IssueRecordQuery{Workspace: workspace, Access: &access}, "auth-1")
	if err != nil || resolved != f.child {
		t.Fatal("legacy reference normalization failed", resolved, err)
	}
	for _, reload := range []bool{false, true} {
		view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, reload)
		for _, test := range []struct {
			name    string
			user    string
			key     *domain.APIKey
			ids     []string
			comment string
		}{
			{"owner-parents", f.data.Viewer.ID, nil, []string{f.child, f.sibling}, f.comment},
			{"guest", f.guest.ID, nil, []string{f.sibling, f.child}, f.comment},
			{"dev-viewer", "", &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{}}, []string{f.child}, f.comment},
			{"selected-team", f.data.Viewer.ID, &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{f.public}}, []string{f.child, f.sibling}, f.comment},
			{"selected-empty", f.data.Viewer.ID, &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{}}, []string{f.child}, "missing"},
			{"normalized-reference", f.data.Viewer.ID, nil, []string{"auth-1", resolved}, ""},
			{"missing-foreign", f.data.Viewer.ID, nil, []string{f.foreignIssue, "missing", f.child + "\x00", strings.ToUpper(f.child)}, "missing"},
			{"empty", f.data.Viewer.ID, nil, nil, ""},
		} {
			t.Run(fmt.Sprintf("reload=%t/%s", reload, test.name), func(t *testing.T) {
				var queryAccess *IssueRecordAccess
				if test.user != "" {
					_, access, err := f.IssueQueryAccess(t.Context(), workspace, test.user)
					if err != nil {
						t.Fatal(err)
					}
					queryAccess = &access
				}
				request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: workspace, Access: queryAccess, Sort: "sortOrder", Direction: "desc",
					TeamIDs: []string{"discarded"}, ProjectIDs: []string{"discarded"}, StateIDs: []string{"discarded"}, Text: "discarded",
					GroupValue: new("discarded"), Limit: 1, Cursor: "discarded"}, IDs: test.ids, CommentIssueID: f.child, CommentID: test.comment}
				if test.key != nil {
					request.Query.AllowedTeamIDs = test.key.TeamIDs
				}
				want, err := scaleP21IssueAuthorizationLegacy(t, f.scaleP09Fixture, test.user, request)
				if err != nil {
					t.Fatal(err)
				}
				read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), test.user, test.key, request)
				if err != nil {
					t.Fatal(err)
				}
				if before, after := scaleP21IssueAuthorizationPolicy(want, test.key, test.user == ""), scaleP21IssueAuthorizationPolicy(read.Data, nil, test.user == ""); !reflect.DeepEqual(before, after) {
					t.Errorf("authorization policy differs:\nlegacy=%+v\nbounded=%+v", before, after)
				}
				if before, after := scaleP21IssueAuthorizationHeaders(want.Issues), scaleP21IssueAuthorizationHeaders(read.Data.Issues); !reflect.DeepEqual(before, after) {
					t.Errorf("selected/ancestor headers differ:\nlegacy=%+v\nbounded=%+v", before, after)
				}
				if !reflect.DeepEqual(want.Comments, read.Data.Comments) || read.Data.Comments == nil {
					t.Errorf("comment headers differ: legacy=%+v bounded=%+v", want.Comments, read.Data.Comments)
				}
				for _, issue := range read.Data.Issues {
					if issue.Description != "" || issue.Title != "" || issue.Creator.AvatarURL != "" || issue.Team.Name != "" {
						t.Fatal("authorization header retained unused issue bodies")
					}
				}
				if view.MetadataWorkUsedForTesting() == 0 || !read.owner.SourceRetainedForTesting() {
					t.Fatal("authorization output has no independent owner/source")
				}
				read.Close()
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("authorization owner leaked or decoded complete metadata")
				}
			})
		}
	}
}

func TestScaleP21IssueAuthorizationLargeIgnoredAndCodec(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: f.data.Workspace.URLKey}, IDs: []string{f.child}, CommentIssueID: f.child, CommentID: f.comment}
	base, err := f.IssueRecord(t.Context(), request.Query.Workspace, f.child)
	if err != nil {
		t.Fatal(err)
	}
	var held int
	for _, copies := range []int{1, 10} {
		issue := base
		issue.Description, issue.Title, issue.Creator.AvatarURL = strings.Repeat("ignored body ", copies*10000), strings.Repeat("ignored title ", copies*10000), strings.Repeat("ignored avatar ", copies*10000)
		raw, _ := json.Marshal(issue)
		raw = append(raw[:len(raw)-1], []byte(`,"CREATOR":{"id":"discard","ID":"`+f.data.Viewer.ID+`"},"PARENTID":null,"parentId":"","RELATIONS":null}`)...)
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, request.Query.Workspace, f.child); err != nil {
			t.Fatal(err)
		}
		comment, _ := json.Marshal(domain.Comment{ID: "ignored-comment-id", Body: strings.Repeat("ignored comment ", copies*10000), User: domain.User{ID: "discard", AvatarURL: issue.Creator.AvatarURL}})
		comment = append(comment[:len(comment)-1], []byte(`,"ID":"decoded-comment","USER":{"ID":"`+f.guest.ID+`"}}`)...)
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, comment, request.Query.Workspace, f.child, f.comment); err != nil {
			t.Fatal(err)
		}
		want, err := scaleP21IssueAuthorizationLegacy(t, f.scaleP09Fixture, f.data.Viewer.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(scaleP21IssueAuthorizationHeaders(want.Issues), scaleP21IssueAuthorizationHeaders(read.Data.Issues)) ||
			!reflect.DeepEqual(want.Comments, read.Data.Comments) {
			t.Fatal("codec aliases, repeated fields, nil normalization or canonical reference refresh differ")
		}
		work := view.MetadataWorkUsedForTesting()
		if held == 0 {
			held = work
		}
		if work != held {
			t.Fatal("tenfold ignored body growth changed authorization admission", held, work)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("large ignored-body read leaked work")
		}
		t.Logf("body_multiplier=%d issue_wire_bytes=%d comment_wire_bytes=%d held_work=%d", copies, len(raw), len(comment), work)
	}
}

func TestScaleP21IssueAuthorizationDecodedPaging(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	var issues []domain.Issue
	var ids []string
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for index := range 501 {
		id := fmt.Sprintf("auth-page-%04d", index)
		ids = append(ids, id)
		issues = append(issues, domain.Issue{ID: id, Identifier: fmt.Sprint("AUTH-", index+1), Number: index + 1,
			Team: f.data.Teams[0], Creator: f.data.Viewer, CreatedAt: at, UpdatedAt: at})
	}
	if err := f.ImportIssues(t.Context(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(issues[499])
	raw = append(raw[:len(raw)-1], []byte(`,"ID":"zz-decoded-last"}`)...)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, f.data.Workspace.URLKey, ids[499]); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		view := scaleP21IssueAuthorizationView(t, f, reload)
		request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: f.data.Workspace.URLKey, Sort: "sortOrder"}, IDs: ids}
		want, err := scaleP21IssueAuthorizationLegacy(t, f, f.data.Viewer.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(want.Issues) != 500 || !reflect.DeepEqual(scaleP21IssueAuthorizationHeaders(want.Issues), scaleP21IssueAuthorizationHeaders(read.Data.Issues)) {
			t.Fatal("second-page cursor used physical rather than decoded last ID", reload, len(want.Issues), len(read.Data.Issues))
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("paged headers leaked work")
		}
	}
}

func TestScaleP21IssueAuthorizationOwnershipAndFailures(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: f.data.Workspace.URLKey}, IDs: []string{f.child}, CommentIssueID: f.child, CommentID: f.comment}
	if read, err := f.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("literal constructor did not refuse bounded ownership", read, err)
	}
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	first, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	held := view.MetadataWorkUsedForTesting()
	second, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	first.Data.Issues[0].Permissions[0].Role = "changed"
	first.Data.TeamSettings[f.public] = domain.TeamSettings{Access: "changed"}
	first.Data.Comments[f.child][0].User.ID = "changed"
	if second.Data.Issues[0].Permissions[0].Role == "changed" || second.Data.TeamSettings[f.public].Access == "changed" || second.Data.Comments[f.child][0].User.ID == "changed" {
		t.Fatal("independent authorization outputs alias")
	}
	first.Close()
	if view.MetadataWorkUsedForTesting() != held || second.Data.Issues[0].ID != f.child {
		t.Fatal("closing one read released another read")
	}
	second.Close()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = held - 1
	if read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("partial quota failure leaked source/header work", read, err, view.MetadataWorkUsedForTesting())
	}
	view.metadataResidency.work.limit = limit
	for _, test := range []string{"cancelled", "missing-workspace", "foreign-user", "too-many", "foreign-snapshot", "corrupt-comment"} {
		t.Run(test, func(t *testing.T) {
			ctx, selected, userID := t.Context(), request, f.data.Viewer.ID
			want := ErrAuthForbidden
			switch test {
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, want = cancelled, context.Canceled
			case "missing-workspace":
				selected.Query.Workspace += "-missing"
			case "foreign-user":
				userID += "-missing"
			case "too-many":
				selected.IDs, want = make([]string, 1001), ErrIssueQuery
			case "foreign-snapshot":
				ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: view.db, workspace: "foreign"})
				want = ErrMetadataRecordQuery
			case "corrupt-comment":
				var original []byte
				if err := f.db.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, request.Query.Workspace, f.child, f.comment).Scan(&original); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, err := f.db.ExecContext(context.Background(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, original, request.Query.Workspace, f.child, f.comment)
					if err != nil {
						t.Error("restore owned comment", err)
					}
				})
				if _, err := f.db.ExecContext(ctx, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, []byte(`{"id":`), request.Query.Workspace, f.child, f.comment); err != nil {
					t.Fatal(err)
				}
				want = nil
			}
			read, err := view.BeginIssueWriteAuthorizationRead(ctx, userID, nil, selected)
			if read != nil || err == nil || want != nil && !errors.Is(err, want) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("error boundary returned an incomplete output or leaked work", test, read, err, view.MetadataWorkUsedForTesting())
			}
		})
	}
}

func TestScaleP21IssueAuthorizationTransferLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: f.data.Workspace.URLKey}, IDs: []string{f.child}}
	read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unowned authorization output escaped", err)
	}
	var closeOutput func()
	ctx, cancel := context.WithCancel(WithMetadataOutputOwner(t.Context(), func(close func()) bool { closeOutput = close; return true }))
	read, err = view.BeginIssueWriteAuthorizationRead(ctx, f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(ctx); err != nil || closeOutput == nil {
		t.Fatal(err)
	}
	held := view.MetadataWorkUsedForTesting()
	cancel()
	if held == 0 || view.MetadataWorkUsedForTesting() != held || len(read.Data.Issues) == 0 {
		t.Fatal("cancellation released transferred authorization output")
	}
	closeOutput()
	closeOutput()
	if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
		t.Fatal("containing operation did not release authorization output")
	}
	var nilRead *IssueWriteAuthorizationRead
	ctx = WithMetadataOutputOwner(t.Context(), nil)
	if err := nilRead.TransferOutput(ctx); !errors.Is(err, ErrBoundedMetadataCoverage) || !errors.Is(metadataOutputError(ctx), ErrBoundedMetadataCoverage) {
		t.Fatal("nil/rejected transfer hid its coverage error", err)
	}
}
