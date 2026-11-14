package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These witnesses retain the exact source/atomicity contracts required before
// the customer taxonomy consumers can leave the activation guard.
func TestScaleP21SW22CustomerTaxonomySourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, itemID := f.data.Workspace.URLKey, idgen.New("sw22-status-")
		teamID := idgen.New("sw22-taxonomy-team-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "scale_p21_sw22.taxonomy_team", teamID, nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: teamID, Key: teamID, Name: "Taxonomy team"})
			data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, Access: "public"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		principals := []domain.User{f.data.Viewer}
		for _, role := range []string{"member", "guest"} {
			_, user := scaleP03Join(t, f, "SW22 "+role, idgen.New("sw22-taxonomy-")+"@example.test", role, teamID)
			principals = append(principals, user)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "customerStatuses", "customerTiers"), workspace, "customer_taxonomy.updated", itemID, nil, func(data *domain.Bootstrap) error {
			data.CustomerStatuses = []domain.CustomerStatus{{ID: itemID, Name: "Warm\x00Name", Position: 1}}
			data.CustomerTiers = []domain.CustomerTier{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, principal := range principals {
			for _, restriction := range []string{"session", "all", "selected-empty", "selected-set"} {
				r := httptest.NewRequest(http.MethodGet, "/api/customer-taxonomy", nil)
				r.Header.Set("X-Workspace-Key", workspace)
				ctx := context.WithValue(r.Context(), authUserContextKey{}, principal)
				if restriction != "session" {
					key := domain.APIKey{ID: "sw22-source-key", CreatorID: principal.ID, TeamRestriction: restriction, Scopes: []string{"read", "write", "admin"}}
					if strings.HasPrefix(restriction, "selected") {
						key.TeamRestriction = "selected"
						if restriction == "selected-set" {
							key.TeamIDs = []string{teamID}
						}
					}
					ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
				}
				w := httptest.NewRecorder()
				(&server{store: repo}).listCustomerTaxonomy(w, r.WithContext(ctx))
				var body struct {
					Statuses []domain.CustomerStatus `json:"statuses"`
					Tiers    []domain.CustomerTier   `json:"tiers"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK ||
					len(body.Statuses) != 1 || body.Statuses[0].Name != "Warm\x00Name" || body.Tiers == nil {
					t.Fatalf("installed taxonomy role/key=%s/%s status=%d body=%s err=%v", principal.ID, restriction, w.Code, w.Body.String(), err)
				}
			}
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		canonical, found, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"customerStatuses", "customerTiers"}})
		if err != nil || !found || len(canonical.CustomerStatuses) != 1 || canonical.CustomerStatuses[0].Name != "WarmName" {
			t.Fatal("canonical source transformation changed", found, err, canonical.CustomerStatuses)
		}
		taxonomy := customerTaxonomy{kind: "status"}
		if err := taxonomy.validate(canonical.CustomerStatuses, "", "WarmName", "", taxonomy.defaultColor()); !errors.Is(err, errInvalid) {
			t.Fatal("canonical source no longer rejects transformed duplicate", err)
		}
		installed, ok := repo.WorkspaceMetadataFields(workspace, "customerStatuses")
		if !ok || taxonomy.validate(installed.CustomerStatuses, "", "WarmName", "", taxonomy.defaultColor()) != nil {
			t.Fatal("installed logical source no longer allows distinct accepted name")
		}
		// Both mutation callbacks use the same validation. Merely switching
		// the accessor changes a successful no-op into a duplicate error.
		validate := func(data *domain.Bootstrap) error {
			if err := taxonomy.validate(taxonomy.items(data), "", "WarmName", "", taxonomy.defaultColor()); err != nil {
				return err
			}
			return store.ErrNoMutation
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "customerStatuses"), workspace, "customer_taxonomy.created", "status", nil, validate); err != nil {
			t.Fatal("legacy accepted validation changed", err)
		}
		err = probe.MutateWorkspaceScoped(t.Context(), workspace, "customer_taxonomy.created", "status", nil, store.MetadataRecordScope{Fields: []string{"customerStatuses"}}, validate)
		if !errors.Is(err, errInvalid) || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("source hold disappeared; re-review all taxonomy consumers", err)
		}
		t.Log("installed list preserves warm typed name for admin/member/guest and all key restrictions; canonical callback changes duplicate validation")
	}
}

func TestScaleP21SW22CustomerTaxonomyAtomicScopeHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		targetID, fallbackID := idgen.New("sw22-target-"), idgen.New("sw22-fallback-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "customerStatuses", "customers"), workspace, "customer_taxonomy.updated", targetID, nil, func(data *domain.Bootstrap) error {
			data.CustomerStatuses = []domain.CustomerStatus{{ID: targetID, Name: "Before", Position: 1}, {ID: fallbackID, Name: "Fallback", Position: 0}}
			data.Customers = nil
			for index := range store.MetadataRecordBatchMaxKeys + 25 {
				data.Customers = append(data.Customers, domain.Customer{ID: idgen.New("sw22-customer-"), Name: fmt.Sprintf("Customer %d", index), Status: "Before"})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		called := false
		err = probe.MutateWorkspaceScoped(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, "customer_taxonomy.updated", targetID, nil,
			store.MetadataRecordScope{Fields: []string{"customerStatuses", "customers"}}, func(*domain.Bootstrap) error {
				called = true
				return nil
			})
		if called || !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatal("complete customer scope no longer proves bounded writer blocker", called, err)
		}
		request := func(method, body string) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest(method, "/api/customer-statuses/"+targetID, strings.NewReader(body))
			r.Header.Set("X-Workspace-Key", workspace)
			r.SetPathValue("id", targetID)
			r = r.WithContext(store.ContextWithActor(r.Context(), f.data.Viewer))
			w := httptest.NewRecorder()
			service := &server{store: repo}
			if method == http.MethodDelete {
				service.deleteCustomerTaxonomy(w, r)
			} else {
				service.updateCustomerTaxonomy(w, r)
			}
			return w
		}
		assertAll := func(want string) {
			t.Helper()
			data, ok := repo.WorkspaceMetadataFields(workspace, "customers")
			if !ok || len(data.Customers) != store.MetadataRecordBatchMaxKeys+25 ||
				!slices.ContainsFunc(data.Customers, func(customer domain.Customer) bool { return customer.Status == want }) {
				t.Fatal("owned customer fixture changed", ok, len(data.Customers))
			}
			for _, customer := range data.Customers {
				if customer.Status != want {
					t.Fatal("taxonomy published a partial customer reassignment", customer.ID, customer.Status, want)
				}
			}
		}
		for _, operation := range []struct {
			body, want string
		}{{`{"name":"Renamed"}`, "Renamed"}, {`{"archived":true}`, "Fallback"}} {
			w := request(http.MethodPatch, operation.body)
			if w.Code != http.StatusOK {
				t.Fatal("legal legacy bulk edit failed", w.Code, w.Body.String())
			}
			assertAll(operation.want)
		}
		var restoreIDs []string
		data, _ := repo.WorkspaceMetadataFields(workspace, "customers")
		for _, customer := range data.Customers {
			restoreIDs = append(restoreIDs, customer.ID)
		}
		restore, _ := json.Marshal(map[string]any{"archived": false, "restoreCustomerIds": restoreIDs})
		if w := request(http.MethodPatch, string(restore)); w.Code != http.StatusOK {
			t.Fatal("legal legacy bulk restore failed", w.Code, w.Body.String())
		}
		assertAll("Renamed")
		if w := request(http.MethodDelete, ""); w.Code != http.StatusNoContent {
			t.Fatal("legal legacy bulk delete failed", w.Code, w.Body.String())
		}
		assertAll("Fallback")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "customerStatuses"), workspace, "customer_taxonomy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.CustomerStatuses = nil
			for index := range store.MetadataRecordBatchMaxKeys + 25 {
				data.CustomerStatuses = append(data.CustomerStatuses, domain.CustomerStatus{ID: idgen.New("sw22-option-"), Name: fmt.Sprintf("Option %d", index), Position: float64(index)})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		called = false
		err = probe.MutateWorkspaceScoped(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, "customer_taxonomy.created", "status", nil,
			store.MetadataRecordScope{Fields: []string{"customerStatuses"}}, func(*domain.Bootstrap) error {
				called = true
				return nil
			})
		if called || !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatal("complete taxonomy scope no longer proves creator blocker", called, err)
		}
		create := httptest.NewRequest(http.MethodPost, "/api/customer-statuses", strings.NewReader(`{"name":"Next option"}`))
		create.Header.Set("X-Workspace-Key", workspace)
		create = create.WithContext(store.ContextWithActor(create.Context(), f.data.Viewer))
		created := httptest.NewRecorder()
		(&server{store: repo}).createCustomerTaxonomy(created, create)
		var item domain.CustomerStatus
		if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil || created.Code != http.StatusCreated || item.Position != float64(store.MetadataRecordBatchMaxKeys+25) {
			t.Fatal("legal legacy taxonomy append changed", created.Code, item.Position, created.Body.String(), err)
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("scope witness decoded complete metadata")
		}
		t.Logf("legacy rename/archive/restore/delete each atomically updates all %d customers and creates after 75 options; complete scoped writer rejects before callback", len(restoreIDs))
	}
}
