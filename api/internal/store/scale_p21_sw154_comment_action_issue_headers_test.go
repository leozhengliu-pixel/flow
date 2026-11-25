package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW154Headers(issues []domain.Issue, request IssueWriteAuthorizationRequest) []domain.Issue {
	headers := scaleP21SW114FullHeaders(issues, request.FullPermissions)
	if request.CommentActionValidation {
		for index, issue := range issues {
			headers[index].Priority = issue.Priority
			if issue.Attachments != nil {
				headers[index].Attachments = make([]domain.Attachment, len(issue.Attachments))
				for attachment, value := range issue.Attachments {
					headers[index].Attachments[attachment].URL = value.URL
				}
			}
		}
	}
	return headers
}

func scaleP21SW154Put(t *testing.T, f scaleP09Fixture, issue domain.Issue, suffix string) {
	t.Helper()
	raw, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	list, err := json.Marshal(issueListProjection(issue))
	if err != nil {
		t.Fatal(err)
	}
	if suffix != "" {
		raw = append(raw[:len(raw)-1], []byte(suffix)...)
		list = append(list[:len(list)-1], []byte(suffix)...)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=?,list_data=? WHERE workspace_key=? AND id=?`,
		raw, list, f.data.Workspace.URLKey, issue.ID); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW154Read(t *testing.T, view *SQLiteStore, f scaleP09Fixture, userID string, key *domain.APIKey, request IssueWriteAuthorizationRequest) *IssueWriteAuthorizationRead {
	t.Helper()
	want, err := scaleP21IssueAuthorizationLegacy(t, f, userID, request)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), userID, key, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	if before, after := scaleP21SW154Headers(want.Issues, request), scaleP21SW154Headers(read.Data.Issues, IssueWriteAuthorizationRequest{
		FullPermissions: true, CommentActionValidation: true,
	}); !reflect.DeepEqual(before, after) {
		if len(before) != len(after) {
			t.Fatalf("optional header counts differ: action=%t full=%t legacy=%d bounded=%d", request.CommentActionValidation, request.FullPermissions, len(before), len(after))
		}
		for index := range before {
			if !reflect.DeepEqual(before[index], after[index]) {
				t.Fatalf("optional header %q differs: action=%t full=%t priority=%d/%d attachments_nil=%t/%t attachments_count=%d/%d",
					before[index].ID, request.CommentActionValidation, request.FullPermissions,
					before[index].Priority, after[index].Priority, before[index].Attachments == nil, after[index].Attachments == nil,
					len(before[index].Attachments), len(after[index].Attachments))
			}
		}
	}
	for _, issue := range read.Data.Issues {
		if issue.Description != "" || issue.Title != "" || issue.DescriptionState != "" || issue.DocumentContent != nil || issue.PriorityLabel != "" {
			t.Fatal("optional header retained an unrelated issue body")
		}
		for _, attachment := range issue.Attachments {
			if !reflect.DeepEqual(attachment, domain.Attachment{URL: attachment.URL}) {
				t.Fatal("comment validation retained attachment metadata")
			}
		}
	}
	if view.MetadataWorkUsedForTesting() == 0 || !read.owner.SourceRetainedForTesting() {
		t.Fatal("optional headers have no independent source/output admission")
	}
	return read
}

func TestScaleP21SW154CommentActionIssueHeaderMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	member := scaleP21SW114Member(t, f)
	workspace := f.data.Workspace.URLKey
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: workspace}, IDs: []string{f.child, f.sibling}}
	existing, err := scaleP21IssueAuthorizationLegacy(t, f.scaleP09Fixture, f.data.Viewer.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	for index, issue := range existing.Issues {
		issue.Priority = index + 1
		issue.Attachments = []domain.Attachment{
			{ID: "ignored-id", URL: "https://example.test/link?x=1&x=2", Title: "ignored", Metadata: map[string]any{"key": "ignored"}},
			{URL: "/uploads/a%20b.json#fragment"},
			{URL: ""},
			{URL: "https://example.test/link?x=1&x=2"},
		}
		issue.Permissions = scaleP21SW114Permissions(issue.ID, f.guest.ID)
		scaleP21SW154Put(t, f.scaleP09Fixture, issue, "")
	}
	for _, reload := range []bool{false, true} {
		view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, reload)
		for _, principal := range []struct{ name, userID string }{
			{"admin-private-outsider", f.data.Viewer.ID}, {"member-private-outsider", member.ID},
			{"guest-explicit-grant", f.guest.ID}, {"development", ""},
		} {
			for _, credential := range []struct {
				name string
				key  *domain.APIKey
			}{
				{"session", nil},
				{"unrestricted", &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "all"}},
				{"selected-empty", &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
				{"selected-public", &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			} {
				t.Run(fmt.Sprintf("reload=%t/%s/%s", reload, principal.name, credential.name), func(t *testing.T) {
					request := IssueWriteAuthorizationRequest{
						Query:          IssueRecordQuery{Workspace: workspace, Sort: "sortOrder", Direction: "desc"},
						IDs:            []string{f.child, f.sibling, f.foreignIssue, "missing", "auth-1", strings.ToUpper(f.child)},
						CommentIssueID: f.child, CommentID: f.comment,
					}
					if principal.userID != "" {
						_, access, err := f.IssueQueryAccess(t.Context(), workspace, principal.userID)
						if err != nil {
							t.Fatal(err)
						}
						request.Query.Access = &access
					}
					if credential.key != nil && credential.key.TeamRestriction == "selected" {
						request.Query.AllowedTeamIDs = credential.key.TeamIDs
					}
					// Read the combined profile first, then the old profiles, so
					// accidental mutation of a shared field map is observable.
					for _, flags := range [][2]bool{{true, true}, {true, false}, {false, true}, {false, false}} {
						request.CommentActionValidation, request.FullPermissions = flags[0], flags[1]
						read := scaleP21SW154Read(t, view, f.scaleP09Fixture, principal.userID, credential.key, request)
						read.Close()
						if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("optional header matrix leaked output or decoded whole metadata")
						}
					}
				})
			}
		}
	}
}

func TestScaleP21SW154CommentActionIssueHeaderCodecAndBounds(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	workspace := f.data.Workspace.URLKey
	base, err := f.IssueRecord(t.Context(), workspace, f.child)
	if err != nil {
		t.Fatal(err)
	}
	base.ParentID = nil
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: workspace}, IDs: []string{f.child}, CommentActionValidation: true}
	for _, suffix := range []string{
		`,"priority":4,"attachments":null}`,
		`,"priority":0,"attachments":[]}`,
		`,"PRIORITY":3,"ATTACHMENTS":[null,{"url":"first","URL":"decoded\u0000url","metadata":{"large":"ignored"}},{"URL":null},{}]}`,
		`,"priority":1,"PRIORITY":null,"attachments":[{"url":"discarded"}],"ATTACHMENTS":null,"attachments":[{"URL":"https://example.test/a?x=\u003c\u0026"}]}`,
	} {
		scaleP21SW154Put(t, f.scaleP09Fixture, base, suffix)
		for _, reload := range []bool{false, true} {
			view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, reload)
			read := scaleP21SW154Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("codec differential retained optional headers")
			}
		}
	}
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	var held int
	for _, copies := range []int{1, 10} {
		issue := base
		issue.Priority = 4
		issue.Title, issue.Description = strings.Repeat("ignored title ", copies*10000), strings.Repeat("ignored body ", copies*10000)
		issue.Attachments = []domain.Attachment{{URL: "https://example.test/fixed", Title: issue.Title,
			Metadata: map[string]any{"unrelated": issue.Description}, Creator: domain.User{AvatarURL: issue.Description}}}
		scaleP21SW154Put(t, f.scaleP09Fixture, issue, "")
		unrelated := issue
		unrelated.ID, unrelated.ParentID = f.sibling, nil
		scaleP21SW154Put(t, f.scaleP09Fixture, unrelated, "")
		read := scaleP21SW154Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
		work := view.MetadataWorkUsedForTesting()
		if held != 0 && work != held {
			t.Fatal("10x unrelated issue/body/attachment metadata changed admission", held, work)
		}
		held = work
		read.Close()
		t.Logf("ignored_body_scale=%d held_work=%d", copies, work)
	}
	previous := 0
	for _, count := range []int{1, 256, 1024} {
		issue := base
		issue.Attachments = make([]domain.Attachment, count)
		for index := range issue.Attachments {
			issue.Attachments[index].URL = fmt.Sprintf("https://example.test/selected/%04d", index)
		}
		scaleP21SW154Put(t, f.scaleP09Fixture, issue, "")
		read := scaleP21SW154Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
		if len(read.Data.Issues) != 1 || len(read.Data.Issues[0].Attachments) != count {
			t.Fatal("legal selected attachment URLs were truncated", count)
		}
		work := view.MetadataWorkUsedForTesting()
		if previous != 0 && work <= previous {
			t.Fatal("selected URL growth was not admitted", count, work, previous)
		}
		previous = work
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("selected URLs retained output or decoded whole metadata")
		}
		t.Logf("selected_urls=%d held_work=%d", count, work)
	}
}

func TestScaleP21SW154CommentActionIssueHeaderOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	workspace := f.data.Workspace.URLKey
	base, err := f.IssueRecord(t.Context(), workspace, f.child)
	if err != nil {
		t.Fatal(err)
	}
	base.ParentID, base.Priority = nil, 4
	base.Attachments = []domain.Attachment{{URL: "https://example.test/canonical"}}
	base.Permissions = scaleP21SW114Permissions(f.child, f.guest.ID)
	scaleP21SW154Put(t, f.scaleP09Fixture, base, "")
	request := IssueWriteAuthorizationRequest{Query: IssueRecordQuery{Workspace: workspace}, IDs: []string{f.child}, CommentActionValidation: true, FullPermissions: true}
	first := scaleP21SW154Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
	pinned, err := first.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	held := view.MetadataWorkUsedForTesting()
	second := scaleP21SW154Read(t, view, f.scaleP09Fixture, f.data.Viewer.ID, nil, request)
	first.Data.Issues[0].Attachments[0].URL = "caller mutation"
	first.Data.Issues[0].Priority = -1
	if second.Data.Issues[0].Attachments[0].URL != base.Attachments[0].URL || second.Data.Issues[0].Priority != 4 {
		t.Fatal("optional header outputs alias")
	}
	second.Close()
	if view.MetadataWorkUsedForTesting() != held {
		t.Fatal("closing a sibling released another optional header output")
	}
	first.Close()
	if read, err := view.BeginIssueWriteAuthorizationRead(pinned, f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("optional headers accepted an expired metadata owner", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginIssueWriteAuthorizationRead(canceled, f.data.Viewer.ID, nil, request); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled optional headers escaped or retained work", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = held - 1
	read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	view.metadataResidency.work.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("partial optional-header quota failure leaked", err)
	}
	unowned, err := view.BeginIssueWriteAuthorizationRead(t.Context(), f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := unowned.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("optional headers escaped without an output owner", err)
	}
	var closeOutput func()
	owned, cancel := context.WithCancel(WithMetadataOutputOwner(t.Context(), func(close func()) bool { closeOutput = close; return true }))
	read, err = view.BeginIssueWriteAuthorizationRead(owned, f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(owned); err != nil || closeOutput == nil {
		t.Fatal("optional header transfer failed", err)
	}
	held = view.MetadataWorkUsedForTesting()
	cancel()
	if held == 0 || view.MetadataWorkUsedForTesting() != held || read.Data.Issues[0].Attachments[0].URL != base.Attachments[0].URL {
		t.Fatal("cancellation released transferred optional headers")
	}
	closeOutput()
	closeOutput()
	if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("containing operation did not release optional headers")
	}
	caller, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("missing caller metadata")
	}
	caller.Issues = []domain.Issue{{ID: f.child, Priority: 1, Attachments: []domain.Attachment{{URL: "forged caller value"}}}}
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
	if read.Data.Issues[0].Priority != 4 || read.Data.Issues[0].Attachments[0].URL != base.Attachments[0].URL {
		t.Fatal("caller metadata replaced canonical comment-validation headers")
	}
	read.Close()
	if _, err := tx.ExecContext(t.Context(), `SELECT 1`); err != nil {
		t.Fatal("optional header reader closed caller transaction", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("borrowed optional headers retained work")
	}
}
