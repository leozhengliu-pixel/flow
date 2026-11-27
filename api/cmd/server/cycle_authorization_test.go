package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Cycles by ID. The literal projection keeps a cycle only for a team
// whose content the viewer reads; cycleAllowed then requires teamAllowed of
// that team. The bounded reader decides it from the unfiltered cycle header.
func TestCycleAuthorizationDifferential(t *testing.T) {
	fx := harnessSetup(t, func(fx *harnessFixture, data *domain.Bootstrap) {
		start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
		cycle := func(id, team string, number int) domain.Cycle {
			return domain.Cycle{ID: id, Number: number, Name: id, TeamID: team, StartsAt: start, EndsAt: start.AddDate(0, 0, 14), Status: "upcoming"}
		}
		p := fx.prefix
		data.Cycles = append(data.Cycles,
			cycle(p+"cycle-public", fx.public, 1),
			cycle(p+"cycle-private", fx.privateTeam, 2),
			cycle(p+"cycle-child", fx.childTeam, 3),
			cycle(p+"cycle-retired", fx.ids["retired-team"], 4),
			cycle(p+"cycle-archived", fx.ids["archived-team"], 5),
			cycle(p+"cycle-no-team", "", 6),
			cycle(p+"cycle-dup", fx.privateTeam, 7),
			cycle(p+"cycle-dup", fx.public, 8),
			cycle(p+"cycle-dup-private", fx.privateTeam, 9),
			cycle(p+"cycle-dup-private", fx.childTeam, 10),
			cycle(p+"cycle/slash", fx.public, 11),
		)
		data.Trash = append(data.Trash, domain.TrashEntry{ID: p + "cycle-trash", ResourceType: "cycle", ResourceID: p + "cycle-trashed", Title: "trashed", Payload: json.RawMessage(`{}`), DeletedAt: fx.archived, ExpiresAt: fx.archived.AddDate(1, 0, 0)})
	})
	p, esc := fx.prefix, url.PathEscape
	rows := []harnessRow{}
	for _, ref := range []struct{ name, id string }{
		{"public", p + "cycle-public"}, {"private", p + "cycle-private"}, {"child", p + "cycle-child"},
		{"retired-team", p + "cycle-retired"}, {"archived-team", p + "cycle-archived"}, {"no-team", p + "cycle-no-team"},
		{"duplicate", p + "cycle-dup"}, {"duplicate-private", p + "cycle-dup-private"}, {"trashed", p + "cycle-trashed"},
		{"missing", p + "cycle-missing"}, {"team-id-as-ref", fx.public},
	} {
		rows = append(rows,
			harnessRow{"get-" + ref.name, http.MethodGet, "GET /api/cycles/{id}", "/api/cycles/" + esc(ref.id), ""},
			harnessRow{"patch-" + ref.name, http.MethodPatch, "PATCH /api/cycles/{id}", "/api/cycles/" + esc(ref.id), `{"name":"x"}`},
			harnessRow{"resource-delete-" + ref.name, http.MethodDelete, "DELETE /api/cycles/{id}/resources/{resourceId}", "/api/cycles/" + esc(ref.id) + "/resources/r", ""},
		)
	}
	rows = append(rows,
		harnessRow{"head-public", http.MethodHead, "GET /api/cycles/{id}", "/api/cycles/" + p + "cycle-public", ""},
		harnessRow{"capacity-get", http.MethodGet, "GET /api/cycles/{id}/capacity", "/api/cycles/" + p + "cycle-private/capacity", ""},
		harnessRow{"capacity-put", http.MethodPut, "PUT /api/cycles/{id}/capacity", "/api/cycles/" + p + "cycle-public/capacity", `{"capacity":3}`},
		harnessRow{"graph", http.MethodGet, "GET /api/cycles/{id}/graph", "/api/cycles/" + p + "cycle-child/graph", ""},
		harnessRow{"calendar-token", http.MethodPost, "POST /api/cycles/{id}/calendar-token", "/api/cycles/" + p + "cycle-public/calendar-token", ""},
		harnessRow{"resources-post", http.MethodPost, "POST /api/cycles/{id}/resources", "/api/cycles/" + p + "cycle-dup/resources", `{}`},
		harnessRow{"start", http.MethodPost, "POST /api/cycles/{id}/start", "/api/cycles/" + p + "cycle-private/start", ""},
		harnessRow{"favorite-patch", http.MethodPatch, "PATCH /api/cycles/{id}", "/api/cycles/" + p + "cycle-public", `{"favorite":true}`},
		harnessRow{"escaped-slash-id", http.MethodGet, "GET /api/cycles/{id}/graph", "/api/cycles/" + esc(p+"cycle/slash") + "/graph", ""},
		harnessRow{"escaped-prefix", http.MethodGet, "GET /api/cycles/{id}", "/%61pi/%63ycles/" + p + "cycle-public", ""},
		harnessRow{"empty-ref", http.MethodGet, "GET /api/cycles/{id}", "/api/cycles//capacity", ""},
	)
	fx.run(t, "SW492", rows, []harnessControl{
		{"get-public/member-private-outsider/session", true},
		{"get-public/guest-explicit/session", true},
		{"get-public/member-private-outsider/selected-empty", false},
		{"get-private/member-private-outsider/session", false},
		{"get-private/sub-team-inheritance/session", true},
		{"get-private/admin/session", true},
		{"get-private/admin-private-outsider/session", false},
		{"get-private/admin/selected-set", false},
		{"get-duplicate/member-private-outsider/session", true},
		{"get-duplicate-private/member-private-outsider/session", false},
		{"get-missing/admin/session", false},
		{"get-trashed/admin/session", false},
		{"get-public/suspended/session", false},
		{"get-public/outsider/session", false},
		{"get-team-id-as-ref/admin/session", false},
	})
}
