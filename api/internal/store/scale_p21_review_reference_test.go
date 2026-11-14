package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21HotReviewReferenceInstalled(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, issueID := f.data.Workspace.URLKey, idgen.New("issue_review_reference_")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	links := make([]string, 1101)
	for index := range links {
		links[index] = issueID
	}
	largeRef := strings.Repeat("long-reference-", 30000)
	if err := f.MutateWorkspace(WithoutIssueRecordMutations(t.Context()), workspace, "scale_p21_review_reference.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = append(data.Issues, domain.Issue{ID: issueID, Identifier: "REF-1", Number: 1, Title: "Visible", Team: data.Teams[0], State: data.States[0], Creator: data.Viewer})
		data.Reviews = []domain.CodeReview{
			{ID: "first", SlugID: "collision", IssueIDs: []string{"missing"}},
			{ID: "collision"},
			{ID: "duplicate", IssueIDs: []string{"missing"}},
			{ID: "duplicate"},
			{ID: "open", SlugID: "open-slug", Description: strings.Repeat("unrelated review body ", 30000)},
			{ID: "empty", IssueIDs: []string{}},
			{ID: "typed\x00id", SlugID: "typed\x00slug"},
			{ID: "long", SlugID: largeRef},
			{ID: "linked", SlugID: "linked-slug", IssueIDs: []string{issueID}},
			{ID: "many", IssueIDs: links},
			{ID: "oversized-link", IssueIDs: []string{strings.Repeat("x", reviewReferenceBatchBytes)}},
		}
		for index := range 75 {
			data.Reviews = append(data.Reviews, domain.CodeReview{ID: fmt.Sprintf("unrelated-%d", index), Description: "retained"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	query := IssueRecordQuery{Workspace: workspace}
	legacy := func(ref string) bool {
		data, found := f.WorkspaceMetadata(workspace)
		if !found {
			return false
		}
		for _, review := range data.Reviews {
			if review.ID == ref || review.SlugID == ref {
				if len(review.IssueIDs) == 0 {
					return true
				}
				visible, err := f.VisibleIssueRecordIDs(t.Context(), query, review.IssueIDs)
				return err == nil && len(visible) > 0
			}
		}
		return false
	}
	for _, ref := range []string{"open", "open-slug", "empty", "collision", "duplicate", "linked", "linked-slug", "many", "typed\x00id", "typed\x00slug", "typedid", "typedslug", "OPEN", "open ", "", "missing", largeRef} {
		got, err := view.InstalledReviewReferenceVisible(t.Context(), workspace, ref, query)
		if err != nil || got != legacy(ref) {
			t.Fatalf("reference length=%d visible=%t want=%t err=%v", len(ref), got, legacy(ref), err)
		}
	}
	if got, err := view.InstalledReviewReferenceVisible(t.Context(), "", "open", query); err != nil || !got {
		t.Fatal("default source changed", got, err)
	}
	var statements int
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "FROM issue_records") {
			statements++
			if view.metadataResidency.work.used < reviewReferenceWork {
				t.Fatal("issue visibility processing lost its admitted work")
			}
		} else {
			t.Fatalf("lookup read metadata SQL: %s", query)
		}
	})
	if got, err := view.InstalledReviewReferenceVisible(ctx, workspace, "many", query); err != nil || !got || statements != 3 {
		t.Fatal("bounded link batches truncated or changed", got, err, statements)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	statements = 0
	ctx = WithSQLStatementObserver(cancelled, func(query string) {
		if strings.Contains(query, "FROM issue_records") {
			statements++
			if statements == 2 {
				cancel()
			}
		}
	})
	if got, err := view.InstalledReviewReferenceVisible(ctx, workspace, "many", query); got || !errors.Is(err, context.Canceled) || statements != 2 {
		t.Fatal("later query error failed to override an earlier visible batch", got, err, statements)
	}
	if got, err := view.InstalledReviewReferenceVisible(t.Context(), workspace, "oversized-link", query); got || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("oversized selected link was truncated or accepted", got, err)
	}
	if got, err := view.InstalledReviewReferenceVisible(t.Context(), workspace, "linked", query); err != nil || !got {
		t.Fatal("lookup failed after cancellation/budget rejection", got, err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("review reference decoded full metadata or leaked work")
	}
}

func TestScaleP21HotReviewReferenceRegistryAndAdmission(t *testing.T) {
	source := &SQLiteStore{
		metadataResidency: newMetadataResidency(), lastWorkspaceKey: "physical\x00default",
		workspaces: map[string]domain.Bootstrap{
			"physical\x00default": {Workspace: domain.Workspace{ID: "default", URLKey: "logical"}, Reviews: []domain.CodeReview{{ID: "default-review"}}},
			"Case":                {Workspace: domain.Workspace{ID: "case", URLKey: "different"}, Reviews: []domain.CodeReview{{ID: "case-review"}}},
			"trail ":              {Workspace: domain.Workspace{ID: "trail", URLKey: "trail"}, Reviews: []domain.CodeReview{{ID: "trailing-review"}}},
		},
	}
	t.Cleanup(source.releaseMetadataLogicalSources)
	for key, data := range source.workspaces {
		logical, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
		if err != nil {
			t.Fatal(err)
		}
		source.installMetadataLogicalSource(key, logical)
	}
	for _, spec := range []struct {
		workspace, ref string
		want           bool
	}{
		{"", "default-review", true}, {"physical\x00default", "default-review", true},
		{"logical", "default-review", false}, {"Case", "case-review", true},
		{"case", "case-review", false}, {"different", "case-review", false},
		{"trail ", "trailing-review", true}, {"trail", "trailing-review", false},
		{"missing", "default-review", false},
	} {
		got, err := source.InstalledReviewReferenceVisible(t.Context(), spec.workspace, spec.ref, IssueRecordQuery{})
		if err != nil || got != spec.want {
			t.Fatalf("workspace=%q ref=%q visible=%t want=%t err=%v", spec.workspace, spec.ref, got, spec.want, err)
		}
	}
	parent, releaseParent, err := source.admitMetadataWork(t.Context(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := source.InstalledReviewReferenceVisible(parent, "", "default-review", IssueRecordQuery{}); err != nil || !got || source.metadataResidency.work.used != 1<<20 {
		t.Fatal("nested lookup borrowed or leaked its parent allowance", got, err)
	}
	releaseParent()
	if _, err := source.InstalledReviewReferenceVisible(parent, "", "default-review", IssueRecordQuery{}); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("expired parent accepted", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := source.InstalledReviewReferenceVisible(cancelled, "", "default-review", IssueRecordQuery{}); err != nil || !got {
		t.Fatal("contextless unlinked-review decision changed after cancellation", got, err)
	}
	_, releaseFull, err := source.admitMetadataWork(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.InstalledReviewReferenceVisible(t.Context(), "", "default-review", IssueRecordQuery{}); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("saturated lookup accepted", err)
	}
	releaseFull()
	source.metadataResidency.mu.Lock()
	logical := source.metadataResidency.logical["Case"]
	delete(source.metadataResidency.logical, "Case")
	source.metadataResidency.mu.Unlock()
	logical.Release()
	if _, err := source.InstalledReviewReferenceVisible(t.Context(), "Case", "case-review", IssueRecordQuery{}); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("missing installed source invented an empty review collection", err)
	}
	if source.metadataResidency.work.used != 0 {
		t.Fatal("lookup errors leaked work")
	}
}
