package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestConfiguredGitAutomationChangesLinkedIssuesAndHonorsBranchOverrides(t *testing.T) {
	now := time.Now().UTC()
	backlog := domain.WorkflowState{ID: "backlog", Name: "Backlog", Type: "backlog", TeamID: "team"}
	started := domain.WorkflowState{ID: "started", Name: "Started", Type: "started", TeamID: "team"}
	done := domain.WorkflowState{ID: "done", Name: "Done", Type: "completed", TeamID: "team"}
	data := domain.Bootstrap{Viewer: domain.User{ID: "u"}, Teams: []domain.Team{{ID: "team"}}, States: []domain.WorkflowState{backlog, started, done}, TeamSettings: map[string]domain.TeamSettings{"team": {PRAutomations: map[string]string{"opened": "started", "merged": "done"}, ProgressOrder: "noAction"}}, Issues: []domain.Issue{{ID: "issue", Team: domain.Team{ID: "team"}, State: backlog}}, Activities: map[string][]domain.ActivityEvent{}, NotificationPreferences: map[string]domain.NotificationPreferences{}}
	review := domain.CodeReview{ID: "review", RepositoryOwner: "owner", RepositoryName: "repo", IssueIDs: []string{"issue"}, BaseBranch: "main"}
	if err := applyGitSettingAutomations(&data, review, "opened", now); err != nil {
		t.Fatal(err)
	}
	if data.Issues[0].State.ID != "started" {
		t.Fatal("open rule did not update the issue")
	}
	data.TargetBranches = []domain.TargetBranch{{TeamID: "team", Repository: "branch", Branch: "release/*", AutomationStates: map[string]string{"merged": ""}}}
	review.BaseBranch = "release/1"
	if err := applyGitSettingAutomations(&data, review, "merged", now); err != nil {
		t.Fatal(err)
	}
	if data.Issues[0].State.ID != "started" {
		t.Fatal("explicit no-action branch rule ignored")
	}
	review.BaseBranch = "main"
	if err := applyGitSettingAutomations(&data, review, "merged", now); err != nil {
		t.Fatal(err)
	}
	if data.Issues[0].State.ID != "done" {
		t.Fatal("nonmatching branch must use team default")
	}
}

func TestStaleSettingsSweepIsBoundedAndIdempotent(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, _ := repo.WorkspaceMetadata("test-workspace")
	team := data.Teams[0]
	now := time.Now().UTC()
	old := now.AddDate(0, -2, 0)
	err = repo.MutateWorkspace(t.Context(), "test-workspace", "settings.test_data", "team", nil, func(data *domain.Bootstrap) error {
		data.States = append(data.States, domain.WorkflowState{ID: "settings-canceled", TeamID: team.ID, Name: "Canceled", Type: "canceled"})
		settings := data.TeamSettings[team.ID]
		settings.AutoCloseStale = true
		settings.StaleMonths = 1
		settings.StaleStatusID = "settings-canceled"
		settings.AutoArchiveMonths = 12
		settings.ProgressOrder = "noAction"
		data.TeamSettings[team.ID] = settings
		for i := 0; i < 125; i++ {
			data.Issues = append(data.Issues, domain.Issue{ID: fmt.Sprintf("settings-old-%d", i), Identifier: fmt.Sprintf("SET-%d", i+1000), Number: i + 1000, Title: "Stale automation test", Team: team, State: domain.WorkflowState{ID: "state_backlog", Name: "Backlog", Type: "backlog"}, CreatedAt: old, UpdatedAt: old})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := server{store: repo}
	if err = s.maintainTeamSettings(t.Context(), "test-workspace", now); err != nil {
		t.Fatal(err)
	}
	page, err := repo.QueryIssueRecords(t.Context(), store.IssueRecordQuery{Workspace: "test-workspace", StateIDs: []string{"settings-canceled"}, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 100 {
		t.Fatalf("sweep must mutate at most 100 stale issues per team: %d", len(page.Items))
	}
	if err = s.maintainTeamSettings(t.Context(), "test-workspace", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	page, err = repo.QueryIssueRecords(t.Context(), store.IssueRecordQuery{Workspace: "test-workspace", StateIDs: []string{"settings-canceled"}, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 125 {
		t.Fatalf("second sweep did not finish pending issues: %d", len(page.Items))
	}
	if err = s.maintainTeamSettings(t.Context(), "test-workspace", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, issue := range page.Items {
		if !issue.AutoClosed {
			t.Fatal("automatic closure marker absent")
		}
	}
}

func TestCustomerDomainPoliciesMatchDomainsWithoutSubstringFalsePositives(t *testing.T) {
	if !customerDomainMatches("User@support.example.com", []string{"example.com"}) || customerDomainMatches("user@notexample.com", []string{"example.com"}) {
		t.Fatal("incorrect customer domain matching")
	}
	if !customerDomainMatches("user@example.com", []string{"USER@EXAMPLE.COM"}) || customerDomainMatches("other@example.com", []string{"user@example.com"}) {
		t.Fatal("email-specific rule must not exclude the entire domain")
	}
}

func TestBusinessHourSLAAndGlobalSwitch(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	friday := time.Date(2026, 3, 6, 16, 0, 0, 0, zone)
	want := time.Date(2026, 3, 9, 10, 0, 0, 0, zone)
	if due := businessDeadline(friday, 120, zone); !due.Equal(want) {
		t.Fatalf("weekend/DST deadline: %s", due)
	}
	if businessMinutes(friday, want, zone) != 120 {
		t.Fatal("remaining business time includes non-working hours")
	}
	data := domain.Bootstrap{Settings: map[string]any{"sla": map[string]any{"enabled": false}}, SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 60}}}
	issue := domain.Issue{ID: "issue", State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, time.Now())
	if len(data.IssueSLAs) != 0 {
		t.Fatal("global SLA disabled still creates timers")
	}
	data.Settings["sla"] = map[string]any{"enabled": true}
	applySLARules(&data, &issue, time.Now())
	if len(data.IssueSLAs) != 1 {
		t.Fatal("enabled SLA did not create timer")
	}
}

func TestUploadPolicyUsesDetectedMediaInsteadOfDeclaredFilename(t *testing.T) {
	settings := domain.WorkspaceSettings{RestrictFileUploads: true, AllowedFileExtensions: []string{"pdf"}}
	if allowedWorkspaceFile(settings, "fake.jpg", "text/html") || !allowedWorkspaceFile(settings, "image.bin", "image/png") || !allowedWorkspaceFile(settings, "REPORT.PDF", "application/pdf") {
		t.Fatal("upload policy does not match allowed media/extensions")
	}
}
