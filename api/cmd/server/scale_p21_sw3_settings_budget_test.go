package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW3PopulatedSettings(t *testing.T, settings domain.WorkspaceSettings) domain.WorkspaceSettings {
	t.Helper()
	stamp := time.Unix(1700000000, 0).UTC()
	value := reflect.ValueOf(&settings).Elem()
	for i := 0; i < value.NumField(); i++ {
		field, definition := value.Field(i), value.Type().Field(i)
		if !field.IsZero() || !strings.Contains(definition.Tag.Get("json"), ",omitempty") {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			field.SetString("sw3-setting")
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				field.Set(reflect.ValueOf([]string{"sw3-setting"}))
			}
		case reflect.Map:
			if field.Type().Key().Kind() == reflect.String && field.Type().Elem().Kind() == reflect.String {
				field.Set(reflect.ValueOf(map[string]string{"sw3-setting": "member"}))
			}
		case reflect.Pointer:
			switch field.Type() {
			case reflect.TypeFor[*time.Time]():
				copy := stamp
				field.Set(reflect.ValueOf(&copy))
			case reflect.TypeFor[*bool]():
				enabled := true
				field.Set(reflect.ValueOf(&enabled))
			}
		case reflect.Bool:
			field.SetBool(true)
		}
	}
	// Each resident policy record fits its own budget; selecting unrelated
	// policy properties together still exceeds the callback's byte budget.
	settings.AgentInstructions = strings.Repeat("unrelated guidance ", 10000)
	settings.AllowedMCPConnectors = strings.Repeat("unrelated connector ", 10000)
	settings.InviteLinksEnabled = true
	settings.FeatureSettings.CustomerManualEdits = true
	settings.SCIMEnabled = false
	settings.SCIMDefaultRole = ""
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(raw, &properties); err != nil {
		t.Fatal(err)
	}
	if len(properties) <= store.MetadataRecordBatchMaxKeys || len(raw) <= store.MetadataRecordPageMaxBytes {
		t.Fatalf("fixture must exceed both whole-settings budgets: properties=%d encoded bytes=%d", len(properties), len(raw))
	}
	return settings
}

func TestScaleP21SW3SettingsPropertyBudgetDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		settings := scaleP21SW3PopulatedSettings(t, f.data.WorkspaceSettings)
		reset := func(manual, invite bool) {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw3.settings_reset", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings = settings
				data.WorkspaceSettings.FeatureSettings.CustomerManualEdits = manual
				data.WorkspaceSettings.InviteLinksEnabled = invite
				data.WorkspaceInviteLink = &domain.WorkspaceInviteLink{Token: "sw3-owned-invite", Enabled: true}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		persisted := func() domain.Bootstrap {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			data, ok := repo.WorkspaceMetadata(workspace)
			if !ok {
				t.Fatal("owned workspace missing")
			}
			return data
		}
		reset(true, true)
		if _, _, err := f.probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}}); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("oversized whole-settings read must exceed the scope budget: %v", err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []string{"manual-allowed", "manual-disabled", "invite-get", "invite-get-disabled", "invite-rotate", "invite-rotate-disabled"} {
				t.Run(operation, func(t *testing.T) {
					var outcomes []any
					for _, s := range []*server{{store: repo}, {store: f.probe}} {
						manual := operation != "manual-disabled"
						invite := !strings.HasSuffix(operation, "-disabled")
						reset(manual, invite)
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						expected := http.StatusOK
						switch operation {
						case "manual-allowed", "manual-disabled":
							if s.allowManualCustomerEdit(w, r) {
								w.WriteHeader(http.StatusNoContent)
							}
							expected = http.StatusNoContent
							if !manual && key == nil {
								expected = http.StatusForbidden
							}
						case "invite-get", "invite-get-disabled":
							s.getWorkspaceInviteLink(w, r)
							if !invite {
								expected = http.StatusForbidden
							}
						case "invite-rotate", "invite-rotate-disabled":
							s.createOrRotateWorkspaceInviteLink(w, r)
							if !invite {
								expected = http.StatusForbidden
							}
						}
						if w.Code != expected {
							t.Fatalf("%s status=%d expected=%d", operation, w.Code, expected)
						}
						data := persisted()
						want := settings
						want.FeatureSettings.CustomerManualEdits, want.InviteLinksEnabled = manual, invite
						if !reflect.DeepEqual(data.WorkspaceSettings, want) {
							t.Fatalf("%s changed unrelated settings", operation)
						}
						rotated := operation == "invite-rotate"
						if rotated && (data.WorkspaceInviteLink.Token == "" || data.WorkspaceInviteLink.Token == "sw3-owned-invite") {
							t.Fatal("invite rotation did not persist a replacement")
						}
						outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, rotated), scaleP21M2JSON(t, data.WorkspaceInviteLink, rotated)})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("%s changed: %s", operation, scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		for _, operation := range []string{"scim-create", "scim-rotate"} {
			t.Run(operation, func(t *testing.T) {
				var outcomes []any
				for _, s := range []*server{{store: repo}, {store: f.probe}} {
					reset(true, true)
					var prior store.SCIMToken
					r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "SW3 budget token"}), httptest.NewRecorder()
					if operation == "scim-rotate" {
						var err error
						prior, err = repo.CreateSCIMToken(t.Context(), f.data.Workspace.ID, "SW3 budget token")
						if err != nil {
							t.Fatal(err)
						}
						r.SetPathValue("id", prior.ID)
						s.rotateSCIMToken(w, r)
					} else {
						s.createSCIMToken(w, r)
					}
					if w.Code != http.StatusCreated {
						t.Fatalf("%s status=%d", operation, w.Code)
					}
					var issued store.SCIMToken
					if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
						t.Fatal(err)
					}
					if issued.ID == "" || issued.Secret == "" || issued.CreatedAt.IsZero() || issued.WorkspaceID != f.data.Workspace.ID {
						t.Fatal("SCIM token response lacks owned credentials")
					}
					tokenID := issued.ID
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
						defer cancel()
						_ = repo.RevokeSCIMToken(ctx, f.data.Workspace.ID, tokenID)
					})
					data := persisted()
					want := settings
					want.SCIMEnabled = true
					if operation == "scim-create" {
						want.SCIMDefaultRole = "member"
					}
					// Rotation intentionally ignores the metadata write error;
					// its 201 alone cannot establish that enabling SCIM committed.
					if !reflect.DeepEqual(data.WorkspaceSettings, want) {
						t.Fatalf("%s failed to persist only the SCIM properties", operation)
					}
					if operation == "scim-rotate" {
						if _, err := repo.AuthenticateSCIMToken(t.Context(), prior.Secret); !errors.Is(err, store.ErrAuthInvalid) {
							t.Fatalf("pre-rotation token remains valid: %v", err)
						}
					}
					auth, authW := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
					auth.Header.Set("Authorization", "Bearer "+issued.Secret)
					gotWorkspace, gotID, ok := s.authenticateSCIMRequest(authW, auth)
					if !ok || gotWorkspace != workspace || gotID != f.data.Workspace.ID {
						t.Fatalf("SCIM authentication failed: status=%d", authW.Code)
					}
					auth.SetPathValue("workspace", workspace+"-missing")
					missingW := httptest.NewRecorder()
					if _, _, ok := s.authenticateSCIMRequest(missingW, auth); ok || missingW.Code != http.StatusForbidden {
						t.Fatalf("cross-workspace SCIM credential accepted: status=%d", missingW.Code)
					}
					reset(true, true)
					auth.SetPathValue("workspace", workspace)
					disabledW := httptest.NewRecorder()
					if _, _, ok := s.authenticateSCIMRequest(disabledW, auth); ok || disabledW.Code != http.StatusForbidden {
						t.Fatalf("disabled SCIM provisioning accepted: status=%d", disabledW.Code)
					}
					if !reflect.DeepEqual(persisted().WorkspaceSettings, settings) {
						t.Fatal("SCIM authentication changed workspace settings")
					}
					issued.ID, issued.Secret, issued.CreatedAt = "<generated-id>", "<generated-secret>", time.Time{}
					outcomes = append(outcomes, []any{w.Code, issued, gotWorkspace, gotID, authW.Code, missingW.Code, scaleP21M2Response(t, missingW, false), disabledW.Code, scaleP21M2Response(t, disabledW, false)})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("%s changed: %s", operation, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("property-scoped handlers decoded full metadata")
		}
	}
}
