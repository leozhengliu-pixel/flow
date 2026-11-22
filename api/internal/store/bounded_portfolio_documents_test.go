package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestBoundedPortfolioDocumentSelection(t *testing.T) {
	body := strings.Repeat("unrelated content ", 1<<18)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "portfolio", URLKey: "portfolio"},
		Documents: []domain.Document{
			{ID: "duplicate", Title: "First occurrence"},
			{ID: "duplicate", PreviousSlugIDs: []string{"old", "current"}, Title: "Historical match"},
			{ID: "target", SlugID: "current", Title: "Exact match"},
			{ID: "other", Content: body},
		},
		Projects: []domain.Project{
			{ID: "reverse", Description: body, Resources: []domain.ProjectResource{{ID: "target"}}},
			{ID: "unrelated", Description: body},
		},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"portfolio": data})
	source, err := view.retainMetadataLogicalSource(t.Context(), "portfolio")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, tc := range []struct {
		ref, title, id string
		ordinal        int
		projects       int
	}{
		{"old", "First occurrence", "duplicate", 0, 0},
		{"current", "Exact match", "target", 2, 1},
		{"missing", "", "missing", -1, 0},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			profile := &portfolioDocumentImage{ref: tc.ref}
			owner := &ResourceRead{store: view}
			defer owner.Close()
			scope, err := profile.resolve(t.Context(), owner, source)
			if err != nil || !slices.Equal(scope.Resources, []string{tc.id}) || profile.documentOrdinal != tc.ordinal {
				t.Fatal("delete did not preserve native reference selection", err)
			}
			selected, err := profile.inputs(t.Context(), owner, source)
			if err != nil || len(selected.Projects) != tc.projects {
				t.Fatal("delete did not select the reciprocal project closure", err)
			}
			if tc.ordinal < 0 && len(selected.Documents) != 0 ||
				tc.ordinal >= 0 && (len(selected.Documents) != 1 || selected.Documents[0].Title != tc.title) {
				t.Fatal("delete selected the wrong document occurrence")
			}
			if view.MetadataWorkUsedForTesting() >= len(body) || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("delete selection decoded unrelated project or document bodies")
			}
			owner.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("document selection leaked its reservations")
			}
		})
	}
}

func TestBoundedPortfolioDocumentRollbackAndReplay(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	peer, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.releaseMetadataLogicalSources()

	const documentID = "portfolio-rollback-document"
	var webhooks, realtime int
	view.SetWebhookSink(func(string, domain.DomainEvent) { webhooks++ })
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { realtime++ })
	before := scaleP09Head(t, f)
	write := func(ctx context.Context) error {
		var releases []func()
		ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
			releases = append(releases, close)
			return true
		})
		defer func() {
			for _, close := range releases {
				close()
			}
		}()
		return view.CreatePortfolioDocument(ctx, workspace, PortfolioDocumentCreate{}, nil, func(data *domain.Bootstrap) (string, error) {
			now := time.Now().UTC()
			data.Documents = []domain.Document{{ID: documentID, Title: "Portfolio document", Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}}
			data.AuditLog = []domain.AuditLogEntry{{ID: "portfolio-rollback-audit", ResourceID: documentID, Action: "created", ResourceType: "document", Actor: data.Viewer, CreatedAt: now}}
			return documentID, nil
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reachedInsert := false
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.HasPrefix(query, "INSERT INTO domain_events") {
			reachedInsert = true
			cancel()
		}
	})
	if err := write(ctx); !reachedInsert || !errors.Is(err, context.Canceled) {
		t.Fatal("document write did not fail at event insertion", err)
	}
	check := func(count int) {
		t.Helper()
		events, err := f.Events(t.Context(), documentID)
		if err != nil || len(events) != count || webhooks != count || realtime != count {
			t.Fatal("document transaction did not publish exactly once on success", err)
		}
		audits, err := f.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: documentID})
		if err != nil || len(audits) != count || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("document transaction leaked audit rows or work on rollback", err)
		}
	}
	read := func(repo *SQLiteStore, count int) {
		t.Helper()
		doc, err := repo.BeginDocumentRead(t.Context(), workspace, "", nil, DocumentReadRequest{Ref: documentID, ReadOnly: true, ProjectionFallback: true})
		if err != nil {
			t.Fatal(err)
		}
		defer doc.Close()
		if len(doc.Data.Documents) != count {
			t.Fatal("installed document generation did not match the committed write")
		}
	}
	check(0)
	read(view, 0)
	if scaleP09Head(t, f) != before {
		t.Fatal("rolled back document advanced the journal")
	}
	if err := write(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(1)
	read(view, 1)
	if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	read(peer, 1)
	if scaleP09Head(t, f) == before {
		t.Fatal("successful document write did not advance the journal")
	}
}
