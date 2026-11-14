package store

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW52PrincipalIdentity(t *testing.T, f scaleP09Fixture, role string, teams []string) domain.User {
	t.Helper()
	user, token, err := f.Register(t.Context(), "SW52 principal "+role, idgen.New("sw52-principal-")+"@example.test", "sw52-principal-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.ErasePersonFromWorkspace(context.Background(), f.data.Workspace.URLKey, user.ID); err != nil {
			t.Errorf("clean up owned membership: %v", err)
		}
		if err := f.EraseAccount(context.Background(), user.ID); err != nil {
			t.Errorf("clean up owned identity: %v", err)
		}
	})
	if err := f.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	invitation, err := f.Invite(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID, user.Email, role, teams)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestScaleP21SW52PrincipalUnknownPolicyDifferentialAndRevocation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	team := f.data.Teams[0].ID
	member := scaleP21SW52PrincipalIdentity(t, f, "member", []string{team})
	guest := scaleP21SW52PrincipalIdentity(t, f, "guest", []string{team})
	admin := scaleP21SW52PrincipalIdentity(t, f, "admin", nil)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "importJobs"), workspace, "scale_p21_sw52.jobs", "", nil, func(data *domain.Bootstrap) error {
		data.ImportJobs = []domain.ImportJob{
			{ID: idgen.New("sw52-owner-job-"), UserID: f.data.Viewer.ID},
			{ID: idgen.New("sw52-member-job-"), UserID: member.ID},
			{ID: idgen.New("sw52-guest-job-"), UserID: guest.ID},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.releaseMetadataLogicalSources()
	source, err := probe.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if source.Stamp().revision >= 0 {
		source.Release()
		t.Fatal("fixture did not retain a genuinely unknown old team-policy source")
	}
	source.Release()
	for _, user := range []domain.User{f.data.Viewer, member, guest, admin} {
		for _, key := range []*domain.APIKey{
			nil,
			{ID: "sw52-unrestricted", Scopes: []string{"read"}, TeamRestriction: "all"},
			{ID: "sw52-empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
			{ID: "sw52-set", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{team}},
		} {
			var oldSQL, newSQL atomic.Int64
			oldCtx := WithSQLStatementObserver(t.Context(), func(string) { oldSQL.Add(1) })
			newCtx := WithSQLStatementObserver(t.Context(), func(string) { newSQL.Add(1) })
			literal, found, err := f.BootstrapForUser(oldCtx, workspace, user.ID)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			want := []string{}
			for _, job := range literal.ImportJobs {
				want = append(want, job.ID)
			}
			before := probe.metadataResidency.work.used
			read, found, err := probe.BeginInstalledPrincipalRead(newCtx, workspace, user.ID, key)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			prepared, err := read.Prepare(newCtx, 1<<20, func(ctx context.Context, raw *InstalledMetadataRead, principal string) (any, error) {
				got := []string{}
				_, err := raw.Visit(ctx, []string{"importJobs"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
					return entry.Value.WithFields(ctx, []string{"id", "userId"}, 0, func(value any) error {
						job := value.(domain.ImportJob)
						if job.UserID == principal {
							got = append(got, job.ID)
						}
						return nil
					})
				})
				return got, err
			})
			if err != nil {
				read.Close()
				t.Fatal(err)
			}
			if err := prepared.Publish(newCtx, workspace, user.ID, key, func(_ context.Context, value any) error {
				if !reflect.DeepEqual(value, want) || probe.metadataResidency.work.used <= before {
					t.Fatal("principal-only owner projection/lifetime changed", value, want)
				}
				return nil
			}); err != nil {
				prepared.Close()
				t.Fatal(err)
			}
			prepared.Close()
			if probe.metadataResidency.work.used != before {
				t.Fatal("principal projection leaked owned work")
			}
			if user.ID == f.data.Viewer.ID && key == nil {
				t.Logf("owner job projection primary SQL literal=%d principal-only=%d", oldSQL.Load(), newSQL.Load())
			}
		}
	}
	if err := f.SetTeamMembership(t.Context(), f.data.Workspace.ID, team, member.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	// Team revocation changes the epoch, but a newly authorized owner-only
	// read can still use its exact older source: no team graph is consulted.
	read, found, err := probe.BeginInstalledPrincipalRead(t.Context(), workspace, member.ID, nil)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	prepared, err := read.Prepare(t.Context(), 1<<20, func(context.Context, *InstalledMetadataRead, string) (any, error) { return "queued", nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Publish(t.Context(), workspace, member.ID, nil, func(context.Context, any) error { t.Fatal("revoked principal emitted"); return nil }); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("queued principal revocation ignored", err)
	}
	if _, _, err := probe.BeginInstalledPrincipalRead(t.Context(), workspace, member.ID, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("suspended principal accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := probe.BeginInstalledPrincipalRead(ctx, workspace, admin.ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled principal accepted", err)
	}
}

func TestScaleP21SW52PrincipalCredentialAndCrossWorkspaceBinding(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.releaseMetadataLogicalSources()
	foreign, _, err := f.Register(t.Context(), "SW52 outsider", idgen.New("sw52-outsider-")+"@example.test", "sw52-outsider-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.EraseAccount(context.Background(), foreign.ID) })
	if _, _, err := f.BootstrapForUser(t.Context(), workspace, foreign.ID); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("literal cross-workspace oracle accepted outsider", err)
	}
	if _, _, err := probe.BeginInstalledPrincipalRead(t.Context(), workspace, foreign.ID, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("principal-only cross-workspace access accepted", err)
	}
	key := &domain.APIKey{ID: "sw52-binding", TeamRestriction: "selected", TeamIDs: []string{}, Scopes: []string{"read"}}
	read, found, err := probe.BeginInstalledPrincipalRead(t.Context(), workspace, f.data.Viewer.ID, key)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	prepared, err := read.PrepareWithIdentity(t.Context(), 1<<20, func(_ context.Context, _ *InstalledMetadataRead, identity InstalledPrincipalIdentity) (any, error) {
		if identity.PrincipalID != f.data.Viewer.ID || identity.ViewerID != f.data.Viewer.ID {
			t.Fatal("principal/viewer identities changed", identity)
		}
		return 7, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	key.TeamIDs = append(key.TeamIDs, f.data.Teams[0].ID)
	if err := prepared.Publish(t.Context(), workspace, f.data.Viewer.ID, key, func(context.Context, any) error { t.Fatal("mutated credential emitted"); return nil }); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("prepared credential binding changed", err)
	}
	if err := prepared.Publish(t.Context(), workspace, f.data.Viewer.ID, nil, func(context.Context, any) error { return nil }); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("failed publication was replayed", err)
	}
	raw, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("missing literal workspace")
	}
	source, err := captureMetadataLogicalSource(t.Context(), raw, copyStamp{workspaceID: idgen.New("old-tenant-origin-"), revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	probe.installMetadataLogicalSource(workspace, source)
	if _, _, err := probe.BeginInstalledPrincipalRead(t.Context(), workspace, f.data.Viewer.ID, nil); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("old physical tenant origin rebound through matching raw payload", err)
	}
}
