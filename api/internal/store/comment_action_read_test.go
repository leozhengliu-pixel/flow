package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW152Data(prefix, ignored string) domain.Bootstrap {
	at := time.Date(2026, 11, 15, 7, 8, 9, 123, time.UTC)
	state, assignee, priority := "state", "", 3
	labels, empty := []string{"label", "label", ""}, []string{}
	return domain.Bootstrap{
		CommentActions: []domain.CommentAction{
			{ID: prefix + "first", IssueID: prefix + "issue", CommentID: prefix + "comment", CommentVersion: 9007199254740993, TargetUserID: "other", Status: "pending", Action: "labels",
				Arguments: domain.CommentActionArguments{StateID: &state, AssigneeID: &assignee, Priority: &priority, LabelIDs: &labels, Title: "native\x00title", Description: "selected description", URL: "https://example.test/link"},
				CreatedAt: at, ResolvedAt: &at, ResolvedBy: "resolver", ResultIssueID: "result"},
			{ID: prefix + "second", IssueID: prefix + "issue", CommentID: prefix + "comment", Status: "accepted", Action: "invalid-but-stored", Arguments: domain.CommentActionArguments{LabelIDs: &empty}, CreatedAt: at},
			{ID: prefix + "third", IssueID: prefix + "issue", CommentID: prefix + "dismissed", Status: "dismissed", Action: "subIssue", CreatedAt: at},
			{ID: prefix + "fourth", IssueID: prefix + "issue", CommentID: "", Status: "unknown", CreatedAt: at},
			{ID: prefix + "global", IssueID: prefix + "elsewhere", CommentID: prefix + "global-comment", Status: "dismissed", Arguments: domain.CommentActionArguments{Description: ignored}, CreatedAt: at},
			{ID: prefix + "empty-issue", IssueID: "", CommentID: prefix + "empty-issue-comment", CreatedAt: at},
			{ID: prefix + "raw-issue", IssueID: prefix + "issue \x00", CommentID: prefix + "raw \x00", CreatedAt: at},
		},
		States: []domain.WorkflowState{{ID: "same", TeamID: "", Name: ignored}, {ID: "same", TeamID: "team", Name: ignored}, {ID: "other", TeamID: "team"}},
		Users:  []domain.User{{ID: "duplicate", Active: false, Name: ignored}, {ID: "duplicate", Active: true, App: true, Name: ignored}, {ID: "", Active: true}},
		Labels: []domain.IssueLabel{{ID: "duplicate", Scope: "workspace", ArchivedAt: &at, Name: ignored}, {ID: "duplicate", Scope: "team", Name: ignored}, {ID: "", Scope: ""}},
		TeamSettings: map[string]domain.TeamSettings{
			"":       {ParentTeamID: "team", Description: ignored},
			"team":   {ParentTeamID: "parent", InheritWorkflowStatuses: true, Description: ignored},
			"parent": {ParentTeamID: "team", Description: ignored},
		},
	}
}

func scaleP21SW152Actions(data domain.Bootstrap, issue string) []domain.CommentAction {
	result := []domain.CommentAction{}
	for _, action := range data.CommentActions {
		if action.IssueID == issue {
			result = append(result, action)
		}
	}
	return result
}

func scaleP21SW152Validation(data domain.Bootstrap) domain.Bootstrap {
	var result domain.Bootstrap
	if data.States != nil {
		result.States = []domain.WorkflowState{}
	}
	for _, state := range data.States {
		result.States = append(result.States, domain.WorkflowState{ID: state.ID, TeamID: state.TeamID})
	}
	if data.Users != nil {
		result.Users = []domain.User{}
	}
	for _, user := range data.Users {
		result.Users = append(result.Users, domain.User{ID: user.ID, Active: user.Active, App: user.App})
	}
	if data.Labels != nil {
		result.Labels = []domain.IssueLabel{}
	}
	for _, label := range data.Labels {
		result.Labels = append(result.Labels, domain.IssueLabel{ID: label.ID, Scope: label.Scope, ArchivedAt: label.ArchivedAt})
	}
	if data.TeamSettings != nil {
		result.TeamSettings = map[string]domain.TeamSettings{}
	}
	for key, settings := range data.TeamSettings {
		result.TeamSettings[key] = domain.TeamSettings{ParentTeamID: settings.ParentTeamID, InheritWorkflowStatuses: settings.InheritWorkflowStatuses}
	}
	return result
}

func scaleP21SW152Parent(t *testing.T, repo *SQLiteStore, workspace string) *InstalledMetadataRead {
	t.Helper()
	parent, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("installed source missing", found, err)
	}
	return parent
}

func TestScaleP21SW152CommentActionsWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw152-")
	fixture := scaleP21SW152Data(prefix, strings.Repeat("excluded body ", 1000))
	restore := ForceFullMutationsForTesting(true)
	err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw152_seed", "", nil, func(data *domain.Bootstrap) error {
		data.CommentActions = fixture.CommentActions
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded", "unknown-revision"} {
		t.Run(state, func(t *testing.T) {
			if state == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "unknown-revision" {
				if err := f.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			parent := scaleP21SW152Parent(t, view, workspace)
			defer parent.Close()
			baseline := view.MetadataWorkUsedForTesting()
			oracle, found := f.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("literal installed source missing")
			}
			statements, pairs := 0, 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
			for _, issue := range []string{prefix + "issue", prefix + "elsewhere", "", prefix + "issue \x00", prefix + "missing"} {
				read, err := parent.ReadCommentActions(ctx, issue)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(read.Actions, scaleP21SW152Actions(oracle, issue)) {
					t.Fatal("raw action fields/order/status/target differ", issue, read.Actions)
				}
				if err := read.LoadValidationHeaders(ctx); err != nil || !reflect.DeepEqual(read.Validation, scaleP21SW152Validation(oracle)) {
					t.Fatal("raw validation directory differs", issue, err)
				}
				used := view.MetadataWorkUsedForTesting()
				if err := read.LoadValidationHeaders(ctx); err != nil || view.MetadataWorkUsedForTesting() != used {
					t.Fatal("repeated header load changed admission", err)
				}
				if issue == prefix+"issue" {
					(*read.Actions[0].Arguments.LabelIDs)[0] = "caller"
				}
				if !read.owner.SourceRetainedForTesting() {
					t.Fatal("selected actions lost their source")
				}
				read.Close()
				read.Close()
				if read.Actions != nil || !reflect.DeepEqual(read.Validation, domain.Bootstrap{}) || view.MetadataWorkUsedForTesting() != baseline {
					t.Fatal("action/header close leaked work")
				}
				pairs++
			}
			for _, comment := range []string{prefix + "comment", prefix + "global-comment", "", prefix + "raw \x00", prefix + "missing"} {
				want := slices.ContainsFunc(oracle.CommentActions, func(action domain.CommentAction) bool { return action.CommentID == comment })
				got, err := parent.CommentActionExists(ctx, comment)
				if err != nil || got != want || view.MetadataWorkUsedForTesting() != baseline {
					t.Fatal("global CommentID existence differs or retains work", comment, want, got, err)
				}
				pairs++
			}
			parent.Close()
			if statements != 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("installed selection queried SQL, hydrated all metadata or leaked", statements)
			}
			t.Logf("state=%s exact_lookup_pairs=%d selection_sql=%d", state, pairs, statements)
		})
	}
}

func TestScaleP21SW152CommentActionsShapesAndDuplicates(t *testing.T) {
	for _, shape := range []string{"nil", "empty", "duplicates"} {
		data := scaleP21SW152Data("", "excluded")
		switch shape {
		case "nil":
			data = domain.Bootstrap{}
		case "empty":
			data = domain.Bootstrap{CommentActions: []domain.CommentAction{}, States: []domain.WorkflowState{}, Users: []domain.User{}, Labels: []domain.IssueLabel{}, TeamSettings: map[string]domain.TeamSettings{}}
		case "duplicates":
			duplicate := data.CommentActions[0]
			duplicate.Status, duplicate.TargetUserID = "dismissed", "another"
			data.CommentActions = append(data.CommentActions, duplicate)
		}
		repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		parent := scaleP21SW152Parent(t, repo, "physical")
		read, err := parent.ReadCommentActions(t.Context(), "issue")
		if err != nil || !reflect.DeepEqual(read.Actions, scaleP21SW152Actions(data, "issue")) {
			t.Fatal(shape, read, err)
		}
		if err := read.LoadValidationHeaders(t.Context()); err != nil || !reflect.DeepEqual(read.Validation, scaleP21SW152Validation(data)) {
			t.Fatal("nil/empty/duplicate validation shape differs", shape, err)
		}
		parent.Close()
		if len(read.Actions) != 0 {
			(*read.Actions[0].Arguments.LabelIDs)[0] = "caller"
		}
		read.Close()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("shape result outlived its owner", shape)
		}
	}
}

func TestScaleP21SW152CommentActionsPinnedLifetimeAndConcurrent(t *testing.T) {
	before := scaleP21SW152Data("", "before")
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before})
	parent := scaleP21SW152Parent(t, repo, "physical")
	held, err := parent.ReadCommentActions(t.Context(), "issue")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	after := cloneBootstrap(before)
	after.CommentActions[0].Arguments.Title = "after"
	after.Users[0].Active = true
	source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	repo.installMetadataLogicalSource("physical", source)
	old, found, err := repo.BeginInstalledMetadataRead(pinned, "physical")
	if err != nil || !found {
		t.Fatal(err)
	}
	old.Close()
	parent.Close()
	if err := held.LoadValidationHeaders(t.Context()); err != nil || !reflect.DeepEqual(held.Validation, scaleP21SW152Validation(before)) ||
		held.Actions[0].Arguments.Title != before.CommentActions[0].Arguments.Title {
		t.Fatal("header loading escaped the selected generation or parent lifetime", err)
	}
	if read, err := parent.ReadCommentActions(t.Context(), "issue"); read != nil || !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed parent remained usable", err)
	}
	held.Close()
	current := scaleP21SW152Parent(t, repo, "physical")
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			read, err := current.ReadCommentActions(t.Context(), "issue")
			if err != nil {
				t.Error(err)
				return
			}
			if err := read.LoadValidationHeaders(t.Context()); err != nil || read.Actions[0].Arguments.Title != "after" || !read.Validation.Users[0].Active {
				t.Error("concurrent selected generation changed", err)
			}
			(*read.Actions[0].Arguments.LabelIDs)[0] = "caller"
			if exists, err := current.CommentActionExists(t.Context(), "global-comment"); err != nil || !exists {
				t.Error("concurrent existence failed", err)
			}
			var closers sync.WaitGroup
			for range 2 {
				closers.Go(read.Close)
			}
			closers.Wait()
		})
	}
	workers.Wait()
	current.Close()
	if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("concurrent readers retained work or hydrated")
	}
}

type scaleP21SW152Cancel struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *scaleP21SW152Cancel) Err() error {
	c.checks++
	if c.checks == 60 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScaleP21SW152CommentActionsCancellationAdmission(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": scaleP21SW152Data("", strings.Repeat("body", 10000))})
	parent := scaleP21SW152Parent(t, repo, "physical")
	defer parent.Close()
	baseline := repo.MetadataWorkUsedForTesting()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := parent.ReadCommentActions(canceled, "issue"); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("initial cancellation escaped", err)
	}
	if found, err := parent.CommentActionExists(canceled, "comment"); found || !errors.Is(err, context.Canceled) {
		t.Fatal("initial existence cancellation escaped", err)
	}
	for _, existence := range []bool{false, true} {
		base, cancel := context.WithCancel(t.Context())
		ctx := &scaleP21SW152Cancel{Context: base, cancel: cancel}
		if existence {
			if found, err := parent.CommentActionExists(ctx, "missing"); found || !errors.Is(err, context.Canceled) {
				t.Fatal("mid-scan existence cancellation escaped", err)
			}
		} else if read, err := parent.ReadCommentActions(ctx, "issue"); read != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("mid-scan selection cancellation escaped", err)
		}
		cancel()
		if repo.MetadataWorkUsedForTesting() != baseline {
			t.Fatal("canceled scan retained partial work")
		}
	}
	limit := repo.metadataResidency.work.limit
	for _, available := range []int{1, 150 << 10} {
		repo.metadataResidency.work.limit = baseline + available
		read, err := parent.ReadCommentActions(t.Context(), "issue")
		repo.metadataResidency.work.limit = limit
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || repo.MetadataWorkUsedForTesting() != baseline {
			t.Fatal("initial/partial quota failure retained selection", available, err)
		}
	}
	read, err := parent.ReadCommentActions(t.Context(), "issue")
	if err != nil {
		t.Fatal(err)
	}
	actionsWork := repo.MetadataWorkUsedForTesting()
	if err := read.LoadValidationHeaders(canceled); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(read.Validation, domain.Bootstrap{}) || repo.MetadataWorkUsedForTesting() != actionsWork {
		t.Fatal("initial header cancellation retained directories/work", err)
	}
	base, cancel := context.WithCancel(t.Context())
	ctx := &scaleP21SW152Cancel{Context: base, cancel: cancel}
	err = read.LoadValidationHeaders(ctx)
	cancel()
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(read.Validation, domain.Bootstrap{}) || repo.MetadataWorkUsedForTesting() != actionsWork {
		t.Fatal("mid-scan header cancellation retained directories/work", err)
	}
	repo.metadataResidency.work.limit = actionsWork + 150<<10
	err = read.LoadValidationHeaders(t.Context())
	repo.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || !reflect.DeepEqual(read.Validation, domain.Bootstrap{}) || repo.MetadataWorkUsedForTesting() != actionsWork {
		t.Fatal("refused headers retained partial directories/work", err)
	}
	if err := read.LoadValidationHeaders(t.Context()); err != nil {
		t.Fatal("header load did not recover after refused admission", err)
	}
	read.Close()
	if repo.MetadataWorkUsedForTesting() != baseline {
		t.Fatal("header retry retained work")
	}
}

func TestScaleP21SW152CommentActionsBodyIndependent(t *testing.T) {
	retained := 0
	for _, count := range []int{100, 1000} {
		data := scaleP21SW152Data("", strings.Repeat("ignored", 2000))
		for range count {
			data.CommentActions = append(data.CommentActions, domain.CommentAction{IssueID: "unrelated", CommentID: "unrelated", Arguments: domain.CommentActionArguments{Description: strings.Repeat("unselected ", 1000)}})
		}
		repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		parent := scaleP21SW152Parent(t, repo, "physical")
		baseline := repo.MetadataWorkUsedForTesting()
		read, err := parent.ReadCommentActions(t.Context(), "issue")
		if err != nil {
			t.Fatal(err)
		}
		work := repo.MetadataWorkUsedForTesting() - baseline
		if retained != 0 && work != retained {
			t.Fatal("unrelated action count changed retained work", retained, work)
		}
		retained = work
		read.Close()
		limit := repo.metadataResidency.work.limit
		repo.metadataResidency.work.limit = baseline + 200<<10
		exists, err := parent.CommentActionExists(t.Context(), "global-comment")
		repo.metadataResidency.work.limit = limit
		if err != nil || !exists || repo.MetadataWorkUsedForTesting() != baseline {
			t.Fatal("header-only existence decoded proposal bodies", err)
		}
		parent.Close()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("body-independent read leaked")
		}
		t.Logf("unrelated_actions=%d retained_action_work=%d existence_available=%d", count, work, 200<<10)
	}
	headerWork := 0
	for _, bytes := range []int{1024, 16384} {
		data := scaleP21SW152Data("", strings.Repeat("x", bytes))
		repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		parent := scaleP21SW152Parent(t, repo, "physical")
		read, err := parent.ReadCommentActions(t.Context(), "missing")
		if err != nil {
			t.Fatal(err)
		}
		before := repo.MetadataWorkUsedForTesting()
		if err := read.LoadValidationHeaders(t.Context()); err != nil {
			t.Fatal(err)
		}
		work := repo.MetadataWorkUsedForTesting() - before
		if headerWork != 0 && headerWork != work {
			t.Fatal("ignored directory bodies changed header work", headerWork, work)
		}
		headerWork = work
		read.Close()
		parent.Close()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("validation header work leaked")
		}
		t.Logf("ignored_directory_bytes=%d retained_header_work=%d", bytes, work)
	}
}

func TestScaleP21SW152CommentActionsOutputOwner(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": scaleP21SW152Data("", "ignored")})
	parent := scaleP21SW152Parent(t, repo, "physical")
	read, err := parent.ReadCommentActions(t.Context(), "issue")
	if err != nil {
		t.Fatal(err)
	}
	if err := read.LoadValidationHeaders(t.Context()); err != nil {
		t.Fatal(err)
	}
	parent.Close()
	var closeOutput func()
	ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool { closeOutput = close; return true })
	if err := read.TransferOutput(ctx); err != nil || closeOutput == nil || repo.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("output transfer released actions/headers", err)
	}
	closeOutput()
	closeOutput()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("output close retained actions/headers")
	}
	parent = scaleP21SW152Parent(t, repo, "physical")
	read, err = parent.ReadCommentActions(t.Context(), "issue")
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	if err := read.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unowned action output escaped", err)
	}
	if err := read.LoadValidationHeaders(t.Context()); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed action read remained usable", err)
	}
}
