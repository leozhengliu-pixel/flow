package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW24ConsentWireRequest(user domain.User, key *domain.APIKey, workspace, role string, cancelled bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/oauth/installation-teams?workspace="+url.QueryEscape(workspace), nil)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	// The unchanged OAuth helper owns session/authentication policy. Supply
	// its existing handoff so this test isolates the final trusted read.
	ctx = context.WithValue(ctx, oauthWorkspaceAccessKey{}, oauthWorkspaceAccess{
		workspace: workspace, userID: user.ID,
		data: domain.Bootstrap{Workspace: domain.Workspace{URLKey: workspace}, Viewer: user, ViewerRole: role},
	})
	if cancelled {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		ctx = cancelled
	}
	return r.WithContext(ctx)
}

func scaleP21SW24ConsentWireOutcome(w *httptest.ResponseRecorder) any {
	return []any{w.Code, w.Header().Get("Content-Type"), w.Body.String()}
}

func scaleP21SW24ConsentWireCompare(t *testing.T, service *server, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	left, right := httptest.NewRecorder(), httptest.NewRecorder()
	service.scaleP21SW24OldConsentTeams(left, request.Clone(request.Context()))
	service.applicationConsentTeams(right, request.Clone(request.Context()))
	if !reflect.DeepEqual(scaleP21SW24ConsentWireOutcome(left), scaleP21SW24ConsentWireOutcome(right)) {
		t.Fatalf("literal consent wire changed bounded=%v: old=%v new=%v", service.store.BoundedMetadataEnabled(), scaleP21SW24ConsentWireOutcome(left), scaleP21SW24ConsentWireOutcome(right))
	}
	return right
}

func TestScaleP21SW24ConsentWireMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw24-consent-wire-")
		at := time.Date(2026, 11, 1, 1, 30, 0, 123456789, time.FixedZone("source wire zone", -4*3600))
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw24.consent_wire", "", nil, func(data *domain.Bootstrap) error {
			for index := range 75 {
				team := domain.Team{
					ID: id + "-" + strconv.Itoa(index), Name: "Name<&>\x00" + string([]byte{0xff, 0xfe}),
					Key: "Key\x00", Color: "Color", Icon: "Icon", Private: index%2 == 0,
					ExternalSource: "hr:org:external\x00", ArchivedByID: "hidden-actor\x00",
					RetiredAt: &at, ArchivedAt: &at, CreatedAt: &at, UpdatedAt: &at,
				}
				data.Teams = append(data.Teams, team)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "teams")
		for _, stage := range []string{"warm", "reloaded"} {
			if stage == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				role, _, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, user.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, source := range []*store.SQLiteStore{repo, probe} {
					for _, cancelled := range []bool{false, true} {
						service := &server{store: source}
						r := scaleP21SW24ConsentWireRequest(user, key, workspace, role, cancelled)
						response := scaleP21SW24ConsentWireCompare(t, service, r)
						want := http.StatusForbidden
						if workspaceAdminRole(role) {
							want = http.StatusOK
							var teams []domain.Team
							if json.Unmarshal(response.Body.Bytes(), &teams) != nil || len(teams) < 75 {
								t.Fatal("complete private/archived directory was lost")
							}
						}
						if response.Code != want {
							t.Fatalf("stage=%s role=%s cancelled=%v status=%d", stage, role, cancelled, response.Code)
						}
					}
				}
			})
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "teams") || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("consent wire read changed canonical teams or decoded complete metadata")
		}
	}
}

func TestScaleP21SW24ConsentWireTimestampErrors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw24-consent-time-")
		base := time.Date(2026, 11, 1, 6, 30, 0, 123456789, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw24.consent_times", "", nil, func(data *domain.Bootstrap) error {
			a, b, c, d := base, base, base, base
			data.Teams = append(data.Teams, domain.Team{ID: id, Name: "Time wire", Key: "TIME", RetiredAt: &a, ArchivedAt: &b, CreatedAt: &c, UpdatedAt: &d})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		dynamic, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		cases := []time.Time{
			{}, time.Now(), base.In(time.Local), base.In(dynamic),
			base.In(time.FixedZone(strings.Repeat("retired-zone", 150000), 3661)),
			base.In(time.FixedZone("offset-seconds", -1)),
			time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
			base.In(time.FixedZone("invalid-positive", 86400)),
			base.In(time.FixedZone("invalid-negative", -86400)),
		}
		for _, source := range []*store.SQLiteStore{repo, probe} {
			metadata, found := source.IssueAccessMetadata(t.Context(), workspace)
			if !found {
				t.Fatal("installed time fixture missing")
			}
			var target *domain.Team
			for index := range metadata.Teams {
				if metadata.Teams[index].ID == id {
					target = &metadata.Teams[index]
				}
			}
			if target == nil {
				t.Fatal("installed time target missing")
			}
			// IssueAccessMetadata exposes the installed timestamp pointers.
			// Replace them only for this same-source wire/error fixture, then
			// restore before any database write or fixture cleanup.
			for _, slot := range []*time.Time{target.RetiredAt, target.ArchivedAt, target.CreatedAt, target.UpdatedAt} {
				saved := *slot
				defer func() { *slot = saved }()
				for _, when := range cases {
					*slot = when
					for _, cancelled := range []bool{false, true} {
						service := &server{store: source}
						r := scaleP21SW24ConsentWireRequest(f.data.Viewer, nil, workspace, "admin", cancelled)
						response := scaleP21SW24ConsentWireCompare(t, service, r)
						if response.Code != http.StatusOK {
							t.Fatal("encoding-error status was normalized")
						}
						if _, err := when.MarshalJSON(); err != nil && response.Body.Len() != 0 {
							t.Fatal("invalid timestamp did not preserve empty JSON output")
						}
						denied := scaleP21SW24ConsentWireRequest(f.data.Viewer, nil, workspace, "member", cancelled)
						if got := scaleP21SW24ConsentWireCompare(t, service, denied); got.Code != http.StatusForbidden {
							t.Fatal("role check moved after wire validation")
						}
					}
				}
				*slot = saved
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("time wire read decoded complete metadata")
		}
	}
}

func TestScaleP21SW24ConsentWireShapes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, teams := range [][]domain.Team{nil, {}, {{}, {ID: "duplicate"}, {ID: "duplicate", Name: "\x00" + string([]byte{0xff})}}} {
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw24.consent_shapes", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = teams
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			probe, err := repo.BoundedMetadataViewForTesting(t.Context())
			sources := []*store.SQLiteStore{repo}
			if err == nil {
				sources = append(sources, probe)
			} else if errors.Is(err, store.ErrBoundedMetadataCoverage) && strings.Contains(err.Error(), `unsplit field "teams"`) {
				// The independent resident loader refuses these accepted
				// legacy shapes. Its pending source contract is not waived.
				t.Logf("resident producer shape remains pending: %v", err)
			} else {
				t.Fatal(err)
			}
			for _, source := range sources {
				for _, key := range []string{workspace, "", workspace + "-missing", strings.ToUpper(workspace), workspace + " "} {
					for _, cancelled := range []bool{false, true} {
						r := scaleP21SW24ConsentWireRequest(f.data.Viewer, nil, key, "admin", cancelled)
						response := scaleP21SW24ConsentWireCompare(t, &server{store: source}, r)
						if response.Code != http.StatusOK {
							t.Fatal("trusted missing/default directory result changed")
						}
					}
				}
			}
		}
	}
}

func (s *server) scaleP21SW24OldConsentTeams(w http.ResponseWriter, r *http.Request) {
	actor := s.oauthRequestUser(r)
	data, ok := s.oauthWorkspaceForRequest(w, r, r.URL.Query().Get("workspace"), actor)
	if !ok {
		return
	}
	if !workspaceAdminRole(data.ViewerRole) {
		writeError(w, 403, "Administrator required to install an application")
		return
	}
	metadata, _ := s.store.WorkspaceMetadata(data.Workspace.URLKey)
	writeJSON(w, 200, metadata.Teams)
}
