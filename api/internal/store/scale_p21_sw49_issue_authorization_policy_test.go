package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW49CommentPolicy(data domain.Bootstrap) any {
	result := struct {
		HIPAA, PreventGuestAgents bool
		Flags                     map[string]bool
		Saved, AgentEnabled       bool
		UserID                    string
		Suggested                 *bool
	}{
		HIPAA: data.WorkspaceSettings.HIPAACompliance, PreventGuestAgents: data.WorkspaceSettings.PreventGuestAgents,
		Flags: map[string]bool{},
	}
	for _, flag := range []string{"ai-agent", "ai"} {
		if enabled, found := data.WorkspaceSettings.FeatureFlags[flag]; found {
			result.Flags[flag] = enabled
		}
	}
	if settings, found := data.UserSettings[data.Viewer.ID]; found {
		result.Saved, result.AgentEnabled, result.UserID, result.Suggested = true, settings.AgentEnabled, settings.UserID, settings.SuggestedActionsOnComments
	}
	return result
}

func TestScaleP21SW49IssueAuthorizationCommentPolicy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
	for _, test := range []struct {
		name       string
		preference domain.UserSettings
		flags      map[string]bool
	}{
		{"saved-zero-nil-flags", domain.UserSettings{}, nil},
		{"empty-user-true-unrelated-flag", domain.UserSettings{AgentEnabled: true, SuggestedActionsOnComments: new(true)}, map[string]bool{"unrelated": false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw49_policy", "", nil, func(data *domain.Bootstrap) error {
				data.UserSettings[userID] = test.preference
				data.WorkspaceSettings.FeatureFlags = test.flags
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, reload := range []bool{false, true} {
				t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
					view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, reload)
					want, err := f.PagedWorkspaceMetadata(t.Context(), workspace, userID)
					if err != nil {
						t.Fatal(err)
					}
					_, access, err := f.IssueQueryAccess(t.Context(), workspace, userID)
					if err != nil {
						t.Fatal(err)
					}
					read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), userID, nil, IssueWriteAuthorizationRequest{
						Query: IssueRecordQuery{Workspace: workspace, Access: &access}, IDs: []string{f.child},
					})
					if err != nil {
						t.Fatal(err)
					}
					defer read.Close()
					if before, after := scaleP21SW49CommentPolicy(want), scaleP21SW49CommentPolicy(read.Data); !reflect.DeepEqual(before, after) {
						t.Fatalf("comment policy differs:\nlegacy=%+v\nbounded=%+v", before, after)
					}
					if settings := read.Data.UserSettings[userID]; settings.UserID != "" || settings.AgentEnabled != test.preference.AgentEnabled ||
						!reflect.DeepEqual(settings.SuggestedActionsOnComments, test.preference.SuggestedActionsOnComments) ||
						len(read.Data.WorkspaceSettings.FeatureFlags) != 0 {
						t.Fatal("saved empty UserID, pointer presence or absent flags changed")
					}
				})
			}
		})
	}
	t.Run("borrowed-caller-fallback", func(t *testing.T) {
		view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
		caller, found := f.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("missing caller metadata")
		}
		caller.WorkspaceSettings.HIPAACompliance, caller.WorkspaceSettings.PreventGuestAgents = false, false
		caller.WorkspaceSettings.FeatureFlags = map[string]bool{"ai": false}
		caller.UserSettings[userID] = domain.UserSettings{}
		_, access, err := f.IssueQueryAccess(t.Context(), workspace, userID)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{
			database: f.db, workspace: workspace, reader: tx, metadata: caller,
		})
		want, err := f.PagedWorkspaceMetadata(ctx, workspace, userID)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginIssueWriteAuthorizationRead(ctx, userID, nil, IssueWriteAuthorizationRequest{
			Query: IssueRecordQuery{Workspace: workspace, Access: &access}, IDs: []string{f.child},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		if before, after := scaleP21SW49CommentPolicy(want), scaleP21SW49CommentPolicy(read.Data); !reflect.DeepEqual(before, after) {
			t.Fatalf("borrowed fallback differs:\nlegacy=%+v\nbounded=%+v", before, after)
		}
		if _, err := tx.ExecContext(ctx, `SELECT 1`); err != nil {
			t.Fatal("authorization reader closed caller transaction", err)
		}
	})
}

func TestScaleP21SW49CurrentCommentActionSettings(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	_, access, err := f.IssueQueryAccess(t.Context(), workspace, userID)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginIssueWriteAuthorizationRead(t.Context(), userID, nil, IssueWriteAuthorizationRequest{
		Query: IssueRecordQuery{Workspace: workspace, Access: &access}, IDs: []string{f.child},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	snapshot := scaleP21SW49CommentPolicy(read.Data)
	bound, err := read.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(bound)
	cancel()
	for _, test := range []struct {
		name       string
		saved      bool
		preference domain.UserSettings
	}{
		{"newer-saved", true, domain.UserSettings{UserID: userID, AgentEnabled: true, SuggestedActionsOnComments: new(true)}},
		{"saved-empty-user", true, domain.UserSettings{AgentEnabled: true}},
		{"missing", false, domain.UserSettings{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw49_current_settings", "", nil, func(data *domain.Bootstrap) error {
				if test.saved {
					settings := test.preference
					settings.AgentInstructions = strings.Repeat("ignored instructions ", 1000)
					data.UserSettings[userID] = settings
				} else {
					delete(data.UserSettings, userID)
				}
				data.UserSettings[userID+"-other"] = domain.UserSettings{UserID: userID, AgentEnabled: true}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			_, current, workspaceFound := f.AccountCollections(workspace, userID)
			settings, saved, err := read.CurrentCommentActionSettings(cancelled, userID)
			if err != nil || saved != test.saved || !workspaceFound {
				t.Fatal("current exact-key settings presence differs", saved, err)
			}
			if settings.UserID != current.UserID || settings.AgentEnabled != current.AgentEnabled ||
				!reflect.DeepEqual(settings.SuggestedActionsOnComments, current.SuggestedActionsOnComments) ||
				settings.AgentInstructions != "" {
				t.Fatal("current selected settings differ from AccountCollections")
			}
			want, got := read.Data, read.Data
			if workspaceFound && current.UserID != "" {
				want.UserSettings = map[string]domain.UserSettings{userID: current}
			}
			if saved && settings.UserID != "" {
				got.UserSettings = map[string]domain.UserSettings{userID: settings}
			}
			if !reflect.DeepEqual(scaleP21SW49CommentPolicy(want), scaleP21SW49CommentPolicy(got)) ||
				!reflect.DeepEqual(snapshot, scaleP21SW49CommentPolicy(read.Data)) {
				t.Fatal("current override changed snapshot or empty/missing fallback")
			}
		})
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("current selected settings leaked output work")
	}
	if _, _, err := read.CurrentCommentActionSettings(t.Context(), userID); err == nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("closed authorization reader admitted current settings")
	}
}
