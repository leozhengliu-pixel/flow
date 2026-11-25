package store

import (
	"context"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW318BorrowedIssueSourceDiscriminator(t *testing.T) {
	db, foreign := &sqlDatabase{}, &sqlDatabase{}
	repo := &SQLiteStore{db: db}
	for _, row := range []struct {
		name, request string
		source        workspaceRead
		present, want bool
	}{
		{name: "absent", request: "workspace"},
		{name: "cleared", request: "workspace", present: true},
		{name: "matching-nil", request: "workspace", present: true, source: workspaceRead{database: db, workspace: "workspace"}},
		{name: "matching-empty", request: "workspace", present: true, source: workspaceRead{database: db, workspace: "workspace", metadata: domain.Bootstrap{Issues: []domain.Issue{}}}},
		{name: "matching-nonempty", request: "workspace", present: true, want: true, source: workspaceRead{database: db, workspace: "workspace", metadata: domain.Bootstrap{Issues: []domain.Issue{{ID: "issue"}}}}},
		{name: "default-matches", present: true, want: true, source: workspaceRead{database: db, workspace: "workspace", metadata: domain.Bootstrap{Issues: []domain.Issue{{ID: "issue"}}}}},
		{name: "foreign-workspace", request: "other", present: true, source: workspaceRead{database: db, workspace: "workspace", metadata: domain.Bootstrap{Issues: []domain.Issue{{ID: "issue"}}}}},
		{name: "foreign-database", request: "workspace", present: true, source: workspaceRead{database: foreign, workspace: "workspace", metadata: domain.Bootstrap{Issues: []domain.Issue{{ID: "issue"}}}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx := t.Context()
			if row.present {
				ctx = context.WithValue(ctx, workspaceReadKey{}, row.source)
			}
			statements := 0
			ctx = WithSQLStatementObserver(ctx, func(string) { statements++ })
			if got := repo.BorrowedWorkspaceReadHasIssues(ctx, row.request); got != row.want || statements != 0 {
				t.Fatal("borrowed discriminator changed matching or loaded storage", got, row.want, statements)
			}
			if len(row.source.metadata.Issues) != 0 && row.source.metadata.Issues[0].ID != "issue" {
				t.Fatal("discriminator mutated its caller source")
			}
		})
	}
}
