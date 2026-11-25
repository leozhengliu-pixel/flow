package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW210RelationProfileValidation(t *testing.T) {
	before := domain.Bootstrap{
		Issues:   []domain.Issue{{ID: "source"}, {ID: "target"}},
		Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
		Notifications: []domain.Notification{}, NotificationDeliveries: []domain.NotificationDelivery{},
	}
	for _, test := range []struct {
		name   string
		change func(*domain.Bootstrap)
		allow  bool
	}{
		{"relations-parent-family", func(data *domain.Bootstrap) {
			id := "source"
			data.Issues[1].ParentID = &id
			data.Issues[0].SubIssueIDs = []string{"target"}
			data.Issues[0].Relations = []domain.IssueRelation{{ID: "relation", IssueID: "source", RelatedIssueID: "target", Type: "parent_of"}}
			data.Activities["source"] = []domain.ActivityEvent{{ID: "activity", Type: "issue.relation_added"}}
		}, true},
		{"body", func(data *domain.Bootstrap) { data.Issues[0].Description = "unexpected" }, false},
		{"version", func(data *domain.Bootstrap) { data.Issues[0].Version++ }, false},
		{"issue-order", func(data *domain.Bootstrap) { data.Issues[0], data.Issues[1] = data.Issues[1], data.Issues[0] }, false},
		{"issue-add", func(data *domain.Bootstrap) { data.Issues = append(data.Issues, domain.Issue{ID: "third"}) }, false},
		{"comment", func(data *domain.Bootstrap) { data.Comments["source"] = []domain.Comment{{ID: "new"}} }, false},
		{"notification", func(data *domain.Bootstrap) { data.Notifications = []domain.Notification{{ID: "new"}} }, false},
		{"delivery", func(data *domain.Bootstrap) { data.NotificationDeliveries = []domain.NotificationDelivery{{ID: "new"}} }, false},
		{"unscoped-activity", func(data *domain.Bootstrap) {
			data.Activities["third"] = []domain.ActivityEvent{{ID: "new", Type: "issue.relation_added"}}
		}, false},
		{"other-activity", func(data *domain.Bootstrap) {
			data.Activities["source"] = []domain.ActivityEvent{{ID: "new", Type: "issue.updated"}}
		}, false},
		{"metadata", func(data *domain.Bootstrap) { data.Teams = []domain.Team{{ID: "new"}} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := cloneBootstrap(before)
			test.change(&after)
			err := validateScopedIssueRelation(before, after)
			if test.allow && err != nil || !test.allow && !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatalf("allowed=%t error=%v", test.allow, err)
			}
		})
	}
}

func TestScaleP21SW210RelationAdmissionRollbackAndRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, mode := range []string{"unowned", "quota", "callback-error", "cancel-after-callback", "title"} {
		t.Run(mode, func(t *testing.T) {
			f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Relation admission"}}, nil)
			workspace := f.data.Workspace.URLKey
			target := issue
			target.ID, target.Identifier, target.Title = issue.ID+"-target", issue.Identifier+"-target", "Relation target"
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw210_target", "", nil, func(data *domain.Bootstrap) error {
				data.Issues = append(data.Issues, target)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			repo := scaleP21IssuePersistenceView(t, f, true, false)
			before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
			targetBefore := scaleP21SW63BusinessRows(t, repo, workspace, target.ID)
			metadata := scaleP21SW76MetadataImage(t, repo.db, workspace)
			indexes := scaleP21SW98Indexes(t, repo, workspace)
			ctx, cancel := context.WithCancel(ContextWithActor(t.Context(), f.data.Viewer))
			defer cancel()
			ctx = ContextWithIssueRelationMutation(WithIssueRecordMutations(ctx, issue.ID))
			var owner *MetadataJSONRead
			if mode != "unowned" {
				var err error
				owner, err = repo.BeginMetadataJSONRead(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				ctx = owner.Context(ctx)
			}
			limit := repo.metadataResidency.work.limit
			defer func() { repo.metadataResidency.work.limit = limit }()
			if mode == "quota" {
				repo.metadataResidency.work.limit = repo.MetadataWorkUsedForTesting()
			}
			callbackError := errors.New("relation callback refused")
			calls := 0
			err := repo.MutateWorkspace(ctx, workspace, "issue.relation_added", issue.ID,
				domain.RelationCreateInput{RelatedIssueID: target.ID, Type: "related"}, func(data *domain.Bootstrap) error {
					calls++
					for index := range data.Issues {
						if data.Issues[index].ID == issue.ID {
							data.Issues[index].Relations = append(data.Issues[index].Relations, domain.IssueRelation{
								ID: "sw210-relation", IssueID: issue.ID, RelatedIssueID: target.ID, Type: "related",
							})
							if mode == "title" {
								data.Issues[index].Title = "Outside relation profile"
							}
						}
					}
					if mode == "callback-error" {
						return callbackError
					}
					if mode == "cancel-after-callback" {
						cancel()
					}
					return nil
				})
			repo.metadataResidency.work.limit = limit
			expected := ErrBoundedMetadataCoverage
			switch mode {
			case "quota":
				expected = ErrMetadataWorkBudget
			case "callback-error":
				expected = callbackError
			case "cancel-after-callback":
				expected = context.Canceled
			}
			if !errors.Is(err, expected) {
				t.Fatalf("mode=%s calls=%d error=%v want=%v", mode, calls, err, expected)
			}
			if (mode == "quota" || mode == "unowned") && calls != 0 {
				t.Fatal("unadmitted relation ran its callback")
			}
			if !reflect.DeepEqual(before, scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)) ||
				!reflect.DeepEqual(targetBefore, scaleP21SW63BusinessRows(t, repo, workspace, target.ID)) ||
				!reflect.DeepEqual(metadata, scaleP21SW76MetadataImage(t, repo.db, workspace)) ||
				!reflect.DeepEqual(indexes, scaleP21SW98Indexes(t, repo, workspace)) {
				t.Fatal("refused relation changed business rows")
			}
			if owner != nil {
				owner.Close()
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("refused relation retained work")
			}
		})
	}
}
