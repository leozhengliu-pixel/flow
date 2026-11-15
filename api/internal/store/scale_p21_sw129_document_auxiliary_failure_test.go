package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW129DocumentAuxiliaryFailureAfterCapture(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, ref := f.data.Workspace.URLKey, idgen.New("sw129-late-")
	app, err := f.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{
		ClientID: idgen.New("sw129-client-"), Name: "Auxiliary directory app",
		Scopes: []string{"read"}, TeamIDs: []string{f.data.Teams[0].ID}, InstalledBy: f.data.Viewer.ID, Active: true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.ErasePersonFromWorkspace(ctx, workspace, app.UserID); err != nil {
			t.Error(err)
		}
		if err := f.EraseAccount(ctx, app.UserID); err != nil {
			t.Error(err)
		}
	})
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.sw129_late", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: ref, Creator: data.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	request := DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true}
	read, err := view.BeginDocumentAuxiliaryRead(t.Context(), workspace, f.data.Viewer.ID, nil, request)
	if err != nil {
		t.Fatal("live application directory baseline", err)
	}
	baseline := view.MetadataWorkUsedForTesting()
	read.Close()
	canceled, cancel := context.WithCancel(t.Context())
	captured := false
	canceled = WithSQLStatementObserver(canceled, func(string) {
		captured = view.MetadataWorkUsedForTesting() >= 2<<20
		cancel()
	})
	read, err = view.BeginDocumentAuxiliaryRead(canceled, workspace, f.data.Viewer.ID, nil, request)
	cancel()
	if !captured || read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("post-capture cancellation escaped or leaked", captured, err)
	}
	limit := view.metadataResidency.work.limit
	refused, staged := false, 0
	late := WithSQLStatementObserver(t.Context(), func(query string) {
		if refused || !strings.HasPrefix(query, "SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,u.deleted_at,m.role,m.status,m.joined_at") {
			return
		}
		refused = true
		staged = view.MetadataWorkUsedForTesting()
		view.metadataResidency.work.limit = staged
	})
	read, err = view.BeginDocumentAuxiliaryRead(late, workspace, f.data.Viewer.ID, nil, request)
	view.metadataResidency.work.limit = limit
	if !refused || staged <= 2<<20 || read != nil || !errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, ErrAuthForbidden) ||
		view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("late ignored policy admission became a 404 or retained values", refused, staged, err)
	}
	t.Logf("baseline_work=%d late_projection_refusal_work=%d post_capture_cancel=true", baseline, staged)
}
