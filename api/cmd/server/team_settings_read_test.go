package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// GET /api/teams/{id}/settings migrated from the COLD facade to the
// bounded team settings read, served through the real cookie/Bearer
// authentication, authorization and route mux on the legacy store, a warm
// bounded view and a bounded view loaded from SQL: a team with settings, a
// sub-team inheriting its parent's workflow and estimation (also from a
// parent the viewer cannot see), a team without settings (default state from
// its states, or the global states), a private team, a missing team, every
// role, a member outside the private team, a guest and team-restricted keys.
// Responses and every owned row must agree byte for byte; bounded, the route
// does not decode the whole workspace.

func teamSettingsReadHandler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/teams/{id}/settings", s.getTeamSettings)
	return s.withColdMetadataRouteMux(mux, s.authenticate(mux))
}

func TestTeamSettingsRead(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamCreateSetup(t)
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	owner, admin, member, guest, inside := f.principals[0].user, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	seed := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw601.settings", func(data *domain.Bootstrap) {
			data.Teams = append(data.Teams, domain.Team{ID: "sw601-child", Name: "Private child", Key: "S6C"}, domain.Team{ID: "sw601-global", Name: "Global states", Key: "S6G"})
			settings := data.TeamSettings[f.private]
			settings.EstimateType, settings.Timezone, settings.DefaultPriority = "fibonacci", "Europe/Berlin", 2
			data.TeamSettings[f.private] = settings
			data.TeamSettings["sw601-child"] = domain.TeamSettings{TeamID: "sw601-child", ParentTeamID: f.private, InheritIssueEstimation: true, InheritWorkflowStatuses: true, Description: "child"}
			parent := data.TeamSettings["tm-parent"]
			parent.EstimateType, parent.LabelPermission = "tShirt", "owners"
			data.TeamSettings["tm-parent"] = parent
			sub := data.TeamSettings["tm-sub"]
			sub.InheritIssueEstimation = true
			data.TeamSettings["tm-sub"] = sub
			data.States = append(data.States, domain.WorkflowState{ID: "sw601-global-todo", Name: "Todo", Type: "unstarted", Position: 1})
			key := func(id, creator, secret string, teams []string) domain.APIKey {
				item := domain.APIKey{ID: id, Name: id, Prefix: "sw601_" + id, SecretHash: secretHash(secret), CreatorID: creator, CreatedAt: at, TeamIDs: []string{}, TeamRestriction: "all"}
				if teams != nil {
					item.TeamRestriction, item.TeamIDs = "selected", teams
				}
				return item
			}
			data.APIKeys = append(data.APIKeys,
				key("sw601-key-member", member.ID, "sw601-secret-member", nil),
				key("sw601-key-selected", member.ID, "sw601-secret-selected", []string{f.public}),
				key("sw601-key-inside", inside.ID, "sw601-secret-inside", []string{"sw601-child"}))
		})
	}
	type trace struct{ lines []string }
	type step func(*server, *trace)
	current := t
	run := func(steps ...step) func(*server) string {
		return func(s *server) string {
			out := &trace{}
			for _, step := range steps {
				step(s, out)
			}
			return strings.Join(out.lines, "\n")
		}
	}
	get := func(user domain.User, team string) step {
		return func(s *server, out *trace) {
			r := httptest.NewRequest(http.MethodGet, "/api/teams/"+team+"/settings", nil)
			r.Header.Set("X-Workspace-Key", workspace)
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: oAuthClientLookupSession(user)})
			w := httptest.NewRecorder()
			teamSettingsReadHandler(s).ServeHTTP(w, r)
			out.lines = append(out.lines, oauthClientLookupResponse("settings "+user.Name+" "+team, w))
		}
	}
	bearer := func(secret, team string) step {
		return func(s *server, out *trace) {
			r := httptest.NewRequest(http.MethodGet, "/api/teams/"+team+"/settings", nil)
			r.Header.Set("X-Workspace-Key", workspace)
			r.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			teamSettingsReadHandler(s).ServeHTTP(w, r)
			out.lines = append(out.lines, oauthClientLookupResponse("settings key "+secret+" "+team, w))
			oAuthCOLDQuiet(current, s, workspace, secret)
		}
	}
	teams := []string{f.public, f.private, "sw601-child", "tm-parent", "tm-sub", "tm-bare", "tm-triage", "sw601-global", "sw601-missing"}
	every := func(user domain.User) []step {
		var steps []step
		for _, team := range teams {
			steps = append(steps, get(user, team))
		}
		return steps
	}
	cases := []struct {
		name  string
		setup func(*testing.T)
		call  func(*server) string
	}{
		{"owner", seed, run(every(owner)...)},
		{"admin", seed, run(every(admin)...)},
		{"member-outside-private", seed, run(every(member)...)},
		{"guest", seed, run(every(guest)...)},
		{"inside-private", seed, run(every(inside)...)},
		{"keys", seed, run(bearer("sw601-secret-member", f.public), bearer("sw601-secret-member", "tm-parent"), bearer("sw601-secret-selected", f.public), bearer("sw601-secret-selected", "tm-parent"), bearer("sw601-secret-inside", "sw601-child"))},
		{"unseeded", nil, run(get(owner, f.public), get(owner, "tm-bare"), get(member, f.private))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			current = t
			outcomes := f.labelRoutesDifferential(t, "sw601-"+test.name, []string{"teamSettings", "states"}, test.setup, test.call)
			t.Logf("legacy result %s", outcomes[0].Result)
		})
	}
}
