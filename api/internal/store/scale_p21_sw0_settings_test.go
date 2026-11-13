package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW0SettingsPropertiesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	original := f.data.WorkspaceSettings
	value := reflect.ValueOf(&original).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.String && field.String() == "" {
			field.SetString("p21-setting")
		}
		if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Bool {
			flag := true
			field.Set(reflect.ValueOf(&flag))
		}
	}
	original.AgentInstructions = strings.Repeat("unrelated settings ", 10000)
	original.AllowedMCPConnectors = strings.Repeat("unrelated connector ", 10000)
	original.InviteLinksEnabled = true
	original.SCIMEnabled = false
	original.SCIMDefaultRole = ""
	ctx := WithMetadataFields(t.Context(), "workspaceSettings")
	reset := func() {
		t.Helper()
		if err := f.MutateWorkspace(ctx, workspace, "scale_p21_sw0.seed", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings = original
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"workspaceSettings"}}); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatalf("fixture must exceed the complete-field budget: %v", err)
	}
	scope := MetadataRecordScope{
		Read:  []RecordKey{{Kind: "workspaceSettings", ID: "inviteLinksEnabled"}},
		Write: []RecordKey{{Kind: "workspaceSettings", ID: "scimEnabled"}, {Kind: "workspaceSettings", ID: "scimDefaultRole"}},
	}
	mutate := func(data *domain.Bootstrap) error {
		if !data.WorkspaceSettings.InviteLinksEnabled {
			t.Fatal("declared read was omitted")
		}
		data.WorkspaceSettings.SCIMEnabled = true
		data.WorkspaceSettings.SCIMDefaultRole = "member"
		return nil
	}
	if err := f.MutateWorkspaceScoped(ctx, workspace, "scim.enabled", "workspace", nil, scope, mutate); err != nil {
		t.Fatal(err)
	}
	want, _ := f.WorkspaceSettingsMetadata(workspace)
	reset()
	if err := probe.MutateWorkspaceScoped(ctx, workspace, "scim.enabled", "workspace", nil, scope, mutate); err != nil {
		t.Fatal(err)
	}
	got, _ := probe.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(want.WorkspaceSettings, got.WorkspaceSettings) {
		t.Fatal("property writer changed unrelated settings or lost the created optional property")
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("property selection decoded a complete workspace")
	}
	if err := probe.MutateWorkspaceScoped(ctx, workspace, "scim.enabled", "workspace", nil, scope, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.InviteLinksEnabled = !data.WorkspaceSettings.InviteLinksEnabled
		return nil
	}); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatalf("read-only property write accepted: %v", err)
	}
	after, _ := probe.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(got.WorkspaceSettings, after.WorkspaceSettings) {
		t.Fatal("rejected property write changed resident policy")
	}
}

func TestScaleP21SW0SettingsPropertiesSelection(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	stored := domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{
		InviteLinksEnabled: true, AgentInstructions: "keep", SCIMDefaultRole: "guest",
	}}
	scope := MetadataRecordScope{
		Read:  []RecordKey{{Kind: "workspaceSettings", ID: "inviteLinksEnabled"}},
		Write: []RecordKey{{Kind: "workspaceSettings", ID: "scimDefaultRole"}},
	}
	selected := metadataRecordSnapshot(stored, scope, metadataRecordFields(scope))
	if !selected.WorkspaceSettings.InviteLinksEnabled || selected.WorkspaceSettings.AgentInstructions != "" {
		t.Fatal("selection retained undeclared properties or lost a declared property")
	}
	selected.WorkspaceSettings.SCIMDefaultRole = "member"
	restoreMetadataRecordFields(WithMetadataRecords(t.Context(), scope), &selected, stored, metadataRecordFields(scope))
	if selected.WorkspaceSettings.SCIMDefaultRole != "member" || selected.WorkspaceSettings.AgentInstructions != "keep" || !selected.WorkspaceSettings.InviteLinksEnabled {
		t.Fatal("property restoration lost unselected input")
	}
}
