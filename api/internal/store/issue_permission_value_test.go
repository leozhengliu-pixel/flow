package store

import (
	"context"
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

func scaleP21SW114FullHeaders(issues []domain.Issue, full bool) []domain.Issue {
	headers := scaleP21IssueAuthorizationHeaders(issues)
	if full {
		for index := range headers {
			headers[index].Permissions = slices.Clone(issues[index].Permissions)
		}
	}
	return headers
}

func scaleP21SW114Permissions(issueID, subjectID string) []domain.IssuePermission {
	at := time.Date(2026, 11, 14, 1, 2, 3, 456789123, time.UTC)
	offset := time.Date(2026, 11, 13, 23, 4, 5, 987654321, time.FixedZone("permission-input", 5*3600+45*60))
	return []domain.IssuePermission{
		{ID: "duplicate", IssueID: issueID + "\x00native", SubjectType: "user", SubjectID: subjectID, Role: "editor", CreatedAt: at, UpdatedAt: at.Add(3 * time.Nanosecond)},
		{ID: "", IssueID: "different-issue", SubjectType: "team", SubjectID: "", Role: "", CreatedAt: offset, UpdatedAt: offset.Add(time.Second)},
		{ID: "duplicate", IssueID: issueID, SubjectType: "user", SubjectID: subjectID, Role: "viewer", CreatedAt: at.Add(time.Hour), UpdatedAt: at.Add(2 * time.Hour)},
		{},
	}
}

func scaleP21SW114SetPermissions(t *testing.T, f scaleP09Fixture, issue domain.Issue, permissions []domain.IssuePermission, missing bool, rawOverride []byte) {
	t.Helper()
	issue.Permissions = nil
	raw, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	list, err := json.Marshal(issueListProjection(issue))
	if err != nil {
		t.Fatal(err)
	}
	if !missing {
		if rawOverride == nil {
			rawOverride, err = json.Marshal(permissions)
			if err != nil {
				t.Fatal(err)
			}
		}
		appendPermissions := func(body []byte) []byte {
			body = append(body[:len(body)-1], []byte(`,"permissions":`)...)
			body = append(body, rawOverride...)
			return append(body, '}')
		}
		raw, list = appendPermissions(raw), appendPermissions(list)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=?,list_data=? WHERE workspace_key=? AND id=?`,
		raw, list, f.data.Workspace.URLKey, issue.ID); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW114Read(t *testing.T, view *SQLiteStore, f scaleP09Fixture, userID string, key *domain.APIKey, request IssueWriteAuthorizationRequest) *IssueWriteAuthorizationRead {
	t.Helper()
	want, err := scaleP21IssueAuthorizationLegacy(t, f, userID, request)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), userID, key, request)
	if err != nil {
		t.Fatal(err)
	}
	if before, after := scaleP21SW114FullHeaders(want.Issues, request.FullPermissions), scaleP21SW114FullHeaders(read.Data.Issues, true); !reflect.DeepEqual(before, after) {
		read.Close()
		t.Fatalf("full=%t permission values differ: legacy issues=%d bounded issues=%d", request.FullPermissions, len(before), len(after))
	}
	if view.MetadataWorkUsedForTesting() == 0 || !read.owner.SourceRetainedForTesting() {
		read.Close()
		t.Fatal("permission values have no independent output/source owner")
	}
	for _, issue := range read.Data.Issues {
		if issue.Description != "" || issue.Title != "" || issue.Creator.AvatarURL != "" || issue.Team.Name != "" {
			read.Close()
			t.Fatal("full permission mode retained unrelated issue bodies")
		}
	}
	return read
}

func scaleP21SW114Member(t *testing.T, f scaleP21IssueAuthorizationFixture) domain.User {
	t.Helper()
	user, token, err := f.Register(t.Context(), "Permission value member", idgen.New("sw114-")+"@example.test", "sw114-member-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.ErasePersonFromWorkspace(context.Background(), f.data.Workspace.URLKey, user.ID)
		_ = f.EraseAccount(context.Background(), user.ID)
	})
	if err := f.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	invitation, err := f.Invite(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID, user.Email, "member", []string{f.public})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestScaleP21SW114IssuePermissionValueMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	member := scaleP21SW114Member(t, f)
	workspace := f.data.Workspace.URLKey
	child, err := f.IssueRecord(t.Context(), workspace, f.child)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.IssueRecord(t.Context(), workspace, f.sibling)
	if err != nil {
		t.Fatal(err)
	}
	rows := []domain.Issue{child, sibling}
	if child.ParentID != nil {
		parent, err := f.IssueRecord(t.Context(), workspace, *child.ParentID)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, parent)
		if parent.ParentID != nil {
			grand, err := f.IssueRecord(t.Context(), workspace, *parent.ParentID)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, grand)
		}
	}
	for _, shape := range []string{"complete", "nil", "empty", "missing", "folded-duplicate-fields"} {
		for _, issue := range rows {
			permissions := scaleP21SW114Permissions(issue.ID, f.guest.ID)
			var raw []byte
			switch shape {
			case "nil", "missing":
				permissions = nil
			case "empty":
				permissions = []domain.IssuePermission{}
			case "folded-duplicate-fields":
				raw = []byte(`[{"id":"first","ID":"decoded","issueId":"first-issue","ISSUEID":"decoded-issue","subjectType":"user","subjectId":"` + f.guest.ID + `","role":"viewer","createdAt":"2026-11-14T01:02:03.000000004Z","CREATEDAT":null,"updatedAt":"2026-11-14T01:02:03Z","UPDATEDAT":"2026-11-15T01:02:03.123456789+05:45"},{"id":"decoded","issueId":"","subjectType":"team","subjectId":"","role":"editor","createdAt":"0001-01-01T00:00:00Z","updatedAt":null},null]`)
			}
			scaleP21SW114SetPermissions(t, f.scaleP09Fixture, issue, permissions, shape == "missing", raw)
		}
		for _, reload := range []bool{false, true} {
			view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, reload)
			for _, principal := range []struct{ name, userID string }{
				{"admin-private-outsider", f.data.Viewer.ID},
				{"member", member.ID},
				{"guest", f.guest.ID},
				{"development", ""},
			} {
				for _, credential := range []struct {
					name string
					key  *domain.APIKey
				}{
					{"session", nil},
					{"unrestricted", &domain.APIKey{ID: "sw114-all", Scopes: []string{"read"}, TeamRestriction: "all"}},
					{"selected-empty", &domain.APIKey{ID: "sw114-empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
					{"selected-public", &domain.APIKey{ID: "sw114-public", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				} {
					for _, summary := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/reload=%t/%s/%s/summary=%t", shape, reload, principal.name, credential.name, summary), func(t *testing.T) {
							var access *IssueRecordAccess
							if principal.userID != "" {
								_, resolved, err := f.IssueQueryAccess(t.Context(), workspace, principal.userID)
								if err != nil {
									t.Fatal(err)
								}
								access = &resolved
							}
							request := IssueWriteAuthorizationRequest{
								Query: IssueRecordQuery{Workspace: workspace, Access: access, Sort: "sortOrder", Direction: "desc", Summary: summary},
								IDs:   []string{f.child, f.sibling, f.foreignIssue, "missing", f.child + "\x00", strings.ToUpper(f.child)},
							}
							if credential.key != nil && credential.key.TeamRestriction == "selected" {
								request.Query.AllowedTeamIDs = credential.key.TeamIDs
							}
							// The default read follows the full read to prove the shared
							// normal child-header field map was not mutated.
							for _, full := range []bool{true, false} {
								request.FullPermissions = full
								read := scaleP21SW114Read(t, view, f.scaleP09Fixture, principal.userID, credential.key, request)
								read.Close()
								read.Close()
								if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
									t.Fatal("permission mode leaked ownership or decoded metadata")
								}
							}
						})
					}
				}
			}
		}
	}
}

func TestScaleP21SW114IssuePermissionBodyAndListBounds(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	workspace := f.data.Workspace.URLKey
	base, err := f.IssueRecord(t.Context(), workspace, f.child)
	if err != nil {
		t.Fatal(err)
	}
	base.ParentID = nil
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: workspace}, IDs: []string{f.child}, FullPermissions: true}
	permissions := scaleP21SW114Permissions(f.child, f.guest.ID)
	var held int
	for _, copies := range []int{1, 10} {
		issue := base
		issue.Description = strings.Repeat("ignored issue body ", copies*10000)
		issue.Title = strings.Repeat("ignored title ", copies*10000)
		issue.Creator.AvatarURL = strings.Repeat("ignored avatar ", copies*10000)
		scaleP21SW114SetPermissions(t, f.scaleP09Fixture, issue, permissions, false, nil)
		read := scaleP21SW114Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
		work := view.MetadataWorkUsedForTesting()
		if held == 0 {
			held = work
		}
		if work != held {
			t.Fatal("10x ignored body growth changed full-permission admission", work, held)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("body differential retained permission output")
		}
		t.Logf("ignored_body_scale=%d held_permission_work=%d", copies, work)
	}
	var previousWork int
	for _, count := range []int{1, 512, 2048} {
		values := make([]domain.IssuePermission, count)
		for index := range values {
			values[index] = permissions[index%len(permissions)]
			values[index].ID = fmt.Sprintf("permission-%05d", index)
			values[index].IssueID = f.child
		}
		scaleP21SW114SetPermissions(t, f.scaleP09Fixture, base, values, false, nil)
		read := scaleP21SW114Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
		if len(read.Data.Issues) != 1 || len(read.Data.Issues[0].Permissions) != count {
			t.Fatal("legal permission list was truncated", count)
		}
		work := view.MetadataWorkUsedForTesting()
		if previousWork != 0 && work <= previousWork {
			t.Fatal("larger selected permission list was not independently admitted", count, work, previousWork)
		}
		previousWork = work
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("legal permission list retained work or decoded full metadata")
		}
		t.Logf("selected_permissions=%d held_permission_work=%d", count, work)
	}
}

func TestScaleP21SW114IssuePermissionOwnershipAndScope(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	workspace := f.data.Workspace.URLKey
	issue, err := f.IssueRecord(t.Context(), workspace, f.child)
	if err != nil {
		t.Fatal(err)
	}
	issue.ParentID = nil
	permissions := scaleP21SW114Permissions(f.child, f.guest.ID)
	scaleP21SW114SetPermissions(t, f.scaleP09Fixture, issue, permissions, false, nil)
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: workspace}, IDs: []string{f.child}, FullPermissions: true}
	first := scaleP21SW114Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
	pinned, err := first.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	firstWork := view.MetadataWorkUsedForTesting()
	second := scaleP21SW114Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
	first.Data.Issues[0].Permissions[0].IssueID = "caller mutation"
	first.Data.Issues[0].Permissions[0].CreatedAt = time.Time{}
	if second.Data.Issues[0].Permissions[0].IssueID == "caller mutation" || second.Data.Issues[0].Permissions[0].CreatedAt.IsZero() {
		t.Fatal("full permission outputs alias")
	}
	second.Close()
	if view.MetadataWorkUsedForTesting() != firstWork {
		t.Fatal("closing an independent permission read released its sibling")
	}
	next := slices.Clone(permissions)
	next[0].ID, next[0].IssueID = "new canonical permission", "new canonical issue field"
	scaleP21SW114SetPermissions(t, f.scaleP09Fixture, issue, next, false, nil)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = !data.WorkspaceSettings.HIPAACompliance
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	current, err := view.BeginIssueWriteAuthorizationRead(pinned, f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if current.Data.Issues[0].Permissions[0].ID != next[0].ID ||
		current.Data.WorkspaceSettings.HIPAACompliance != first.Data.WorkspaceSettings.HIPAACompliance {
		t.Fatal("permission flag changed current SQL row or caller metadata-generation scope")
	}
	first.Close()
	if current.Data.Issues[0].Permissions[0].IssueID != next[0].IssueID || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("permission reader borrowed parent output lifetime")
	}
	current.Close()
	if read, err := view.BeginIssueWriteAuthorizationRead(pinned, f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("expired source pin was ignored by full-permission mode", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginIssueWriteAuthorizationRead(canceled, f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled permission read allocated or leaked", err)
	}
	probe, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	currentWork := view.MetadataWorkUsedForTesting()
	probe.Close()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = currentWork - 1
	if read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("permission admission failure leaked", err)
	}
	view.metadataResidency.work.limit = limit
	unowned, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := unowned.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) ||
		view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(unowned.Data, domain.Bootstrap{}) {
		t.Fatal("unowned full-permission output escaped", err)
	}
	var closeOutput func()
	owned := WithMetadataOutputOwner(t.Context(), func(close func()) bool { closeOutput = close; return true })
	owned, cancel = context.WithCancel(owned)
	read, err := view.BeginIssueWriteAuthorizationRead(owned, f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(owned); err != nil || closeOutput == nil {
		t.Fatal("permission output transfer failed", err)
	}
	work := view.MetadataWorkUsedForTesting()
	cancel()
	if work == 0 || view.MetadataWorkUsedForTesting() != work || read.Data.Issues[0].Permissions[0].ID != next[0].ID {
		t.Fatal("cancellation released transferred full permission values")
	}
	closeOutput()
	closeOutput()
	if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("containing operation retained permission output")
	}
	caller, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("missing fixture caller metadata")
	}
	caller.Issues = []domain.Issue{{ID: f.child, Permissions: []domain.IssuePermission{{ID: "forged caller permission"}}}}
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	borrowed := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: view.db, workspace: workspace, reader: tx, metadata: caller})
	read, err = view.BeginIssueWriteAuthorizationRead(borrowed, f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Data.Issues) != 1 || read.Data.Issues[0].Permissions[0].ID != next[0].ID {
		t.Fatal("caller metadata substituted a forged full permission value")
	}
	read.Close()
	if _, err := tx.ExecContext(t.Context(), `SELECT 1`); err != nil {
		t.Fatal("permission reader closed caller SQL snapshot", err)
	}
	foreign := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: view.db, workspace: "foreign", reader: tx, metadata: caller})
	if read, err := view.BeginIssueWriteAuthorizationRead(foreign, f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, ErrMetadataRecordQuery) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("full permission mode accepted a foreign workspace snapshot", err)
	}
}
