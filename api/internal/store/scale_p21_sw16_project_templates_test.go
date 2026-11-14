package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

func TestScaleP21SW16ProjectTemplateInstalledWireRead(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone(strings.Repeat("private-zone", 5000), 7*3600+123))
	outOfOffice := stamp.Add(time.Hour)
	long := strings.Repeat("large template <>&\x00\xff ", 110000)
	creator := domain.User{
		ID: "creator\x00", Name: "native\xff", AppScopes: []string{}, AppTeamIDs: []string{"private-team"},
		OutOfOfficeUntil: &outOfOffice, Via: &domain.ActorBot{ID: "request-only"},
	}
	template := domain.ProjectTemplate{
		ID: "template-native", Name: "Native\xff", Description: long, Creator: creator, CreatedAt: stamp, UpdatedAt: stamp,
		TeamIDs: []string{}, LabelIDs: nil, MemberIDs: []string{"one"}, Milestones: []domain.TemplateMilestone{{ID: "mile", Name: "M"}},
		Issues: []domain.ProjectTemplateIssue{{ID: "one", Title: "Issue", LabelIDs: []string{}}},
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "sw16.template_native", "", nil, func(data *domain.Bootstrap) error {
		data.ProjectTemplates = []domain.ProjectTemplate{template}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	baseline, found := f.BootstrapFor(workspace)
	if !found {
		t.Fatal("legacy source disappeared")
	}
	want, err := json.Marshal(baseline.ProjectTemplates)
	if err != nil {
		t.Fatal(err)
	}
	before := f.WorkspaceMetadataDecodes()
	read, found, err := f.BeginProjectTemplatesRead(t.Context(), workspace)
	if err != nil || !found || read == nil {
		t.Fatal("installed template read failed", found, err)
	}
	got, err := json.Marshal(read.Templates())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("installed native wire value differs", err)
	}
	actual := read.Templates()
	if unsafe.StringData(actual[0].Description) == unsafe.StringData(template.Description) {
		t.Fatal("description retains source backing")
	}
	if actual[0].CreatedAt.Location() == stamp.Location() || actual[0].Creator.OutOfOfficeUntil.Location() == stamp.Location() {
		t.Fatal("timestamp retains arbitrary source zone history/name backing")
	}
	actual[0].MemberIDs[0] = "owned"
	actual[0].Issues[0].LabelIDs = append(actual[0].Issues[0].LabelIDs, "owned")
	current, _ := f.BootstrapFor(workspace)
	if current.ProjectTemplates[0].MemberIDs[0] != "one" || len(current.ProjectTemplates[0].Issues[0].LabelIDs) != 0 {
		t.Fatal("template read shares mutable containers")
	}
	read.Close()
	read.Close()
	if f.WorkspaceMetadataDecodes() != before {
		t.Fatal("template read leaked work or decoded complete metadata")
	}
	// Installed local nil and normalized-on-reload empty use the same SQL
	// root, so SQL collection shape alone cannot reproduce the accessor.
	for _, templates := range [][]domain.ProjectTemplate{nil, {}} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projectTemplates"), workspace, "sw16.template_shape", "", nil, func(data *domain.Bootstrap) error {
			data.ProjectTemplates = templates
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		read, found, err := f.BeginProjectTemplatesRead(t.Context(), workspace)
		if err != nil || !found || !reflect.DeepEqual(read.Templates(), templates) {
			t.Fatal("warm nil/empty template shape differs", found, err)
		}
		read.Close()
	}
}

func TestScaleP21SW16ProjectTemplateNativeTimeWire(t *testing.T) {
	for _, stamp := range []time.Time{
		{}, time.Now(), time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC),
		time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("odd", -3600-123)),
		time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("zero", 0)),
		time.Date(10000, 1, 2, 3, 4, 5, 6, time.FixedZone("wide", 25*3600)),
	} {
		source := []domain.ProjectTemplate{{CreatedAt: stamp, UpdatedAt: stamp, Creator: domain.User{OutOfOfficeUntil: &stamp}}}
		cost, err := scaleP21SW16ProjectTemplateCost(source)
		if err != nil || cost <= 0 {
			t.Fatal("native timestamp preflight failed", err)
		}
		clone := scaleP21SW16CloneTemplateValue(reflect.ValueOf(source)).Interface().([]domain.ProjectTemplate)
		want, oldErr := json.Marshal(source)
		got, newErr := json.Marshal(clone)
		if !bytes.Equal(got, want) || (oldErr == nil) != (newErr == nil) || oldErr != nil && oldErr.Error() != newErr.Error() {
			t.Fatalf("native time wire/error differs: %s/%s, %v/%v", want, got, oldErr, newErr)
		}
	}
	long := strings.Repeat("unrelated backing ", 1<<15)
	values := [1]*string{new(string)}
	*values[0] = long[:3]
	remaining, nodes := scaleP21SW16ProjectTemplateMaxWork, 0
	if err := scaleP21SW16TemplateValueCost(reflect.ValueOf(values), 0, &remaining, &nodes); err != nil {
		t.Fatal("supported fixed-size array preflight failed", err)
	}
	clone := scaleP21SW16CloneTemplateValue(reflect.ValueOf(values)).Interface().([1]*string)
	if !reflect.DeepEqual(clone, values) || clone[0] == values[0] || unsafe.StringData(*clone[0]) == unsafe.StringData(*values[0]) {
		t.Fatal("supported fixed-size array retained mutable/string source backing")
	}
}

func TestScaleP21SW16ProjectTemplateAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "sw16.template_budget", "", nil, func(data *domain.Bootstrap) error {
		data.ProjectTemplates = []domain.ProjectTemplate{{ID: "template", Description: "small"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, found, err := view.BeginProjectTemplatesRead(ctx, workspace); !errors.Is(err, context.Canceled) || read != nil || found {
		t.Fatal("cancelled read cloned payload", read, found, err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	read, found, err := view.BeginProjectTemplatesRead(t.Context(), workspace)
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || read != nil || !found || view.metadataResidency.work.used != 0 {
		t.Fatal("admission shortage cloned/retained payload", read, found, err)
	}
	if _, err := scaleP21SW16ProjectTemplateCost([]domain.ProjectTemplate{{Description: strings.Repeat("x", 5<<20)}}); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("oversized selected templates bypassed preflight", err)
	}
	read, found, err = view.BeginProjectTemplatesRead(t.Context(), workspace)
	if err != nil || !found || read == nil {
		t.Fatal("admission failure did not recover", found, err)
	}
	if view.metadataResidency.work.used == 0 {
		t.Fatal("selected actual policy-only source escaped retained output admission")
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("policy-only read leaked its work allowance")
	}
	if missing, found, err := view.BeginProjectTemplatesRead(t.Context(), workspace+"-missing"); err != nil || found || missing != nil {
		t.Fatal("missing workspace changed", missing, found, err)
	}
}

func TestScaleP21SW16ProjectTemplateBootstrapErrorBoundary(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	issueID := "sw16-template-error-issue"
	if err := f.ImportIssues(t.Context(), workspace, []domain.Issue{{ID: issueID, Identifier: "SW16-ERROR", Title: "Unrelated issue"}}); err != nil {
		t.Fatal(err)
	}
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issueID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, saved, workspace, issueID); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, []byte(`{"id":"sw16-template-error-issue","title":7}`), workspace, issueID); err != nil {
		t.Fatal(err)
	}
	if _, found := f.BootstrapFor(workspace); found {
		t.Fatal("legacy BootstrapFor did not fail on unrelated issue hydration")
	}
	read, found, err := f.BeginProjectTemplatesRead(t.Context(), workspace)
	if err != nil || found || read != nil {
		t.Fatal("template candidate bypassed Bootstrap hydration failure", found, err)
	}
	t.Log("same actual store: legacy BootstrapFor and bounded validation preserve unrelated issue hydration failure")
}

func TestScaleP21SW16ProjectTemplateContentValidation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	for _, kind := range []string{"import_input", "migration_input", "activity", "comment", "notification", "delivery"} {
		t.Run(kind, func(t *testing.T) {
			for index := 0; index < 130; index++ {
				id := fmt.Sprintf("sw16-validation-%03d", index)
				if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`, workspace, kind, "owned", id, "2026-01-02", []byte(`null`)); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if _, err := f.db.ExecContext(context.Background(), `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id='owned'`, workspace, kind); err != nil {
					t.Error(err)
				}
			})
			if _, found := f.BootstrapFor(workspace); !found {
				t.Fatal("valid null content source failed legacy hydration")
			}
			read, found, err := f.BeginProjectTemplatesRead(t.Context(), workspace)
			if err != nil || !found || read == nil {
				t.Fatal("bounded validation failed valid 130-row content", found, err)
			}
			read.Close()
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind=? AND resource_id='owned' AND id='sw16-validation-129'`, []byte(`{"createdAt":"invalid"}`), workspace, kind); err != nil {
				t.Fatal(err)
			}
			if _, found := f.BootstrapFor(workspace); found {
				t.Fatal("legacy ignored malformed unrelated content after two pages")
			}
			read, found, err = f.BeginProjectTemplatesRead(t.Context(), workspace)
			if err != nil || found || read != nil {
				t.Fatal("bounded source validation changed unrelated content failure", read, found, err)
			}
		})
	}
}

func TestScaleP21SW16ProjectTemplateLogicalWorkspace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	const issueID = "sw16-template-logical-issue"
	if err := f.ImportIssues(t.Context(), workspace, []domain.Issue{{ID: issueID, Title: "Physical source"}}); err != nil {
		t.Fatal(err)
	}
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issueID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, saved, workspace, issueID); err != nil {
			t.Error(err)
		}
		if err := f.MutateWorkspace(context.Background(), workspace, "sw16.template_identity_restore", "", nil, func(data *domain.Bootstrap) error {
			data.Workspace.URLKey = workspace
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	if err := f.MutateWorkspace(t.Context(), workspace, "sw16.template_identity_empty", "", nil, func(data *domain.Bootstrap) error {
		data.Workspace.URLKey = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, []byte(`{"title":7}`), workspace, issueID); err != nil {
		t.Fatal(err)
	}
	legacy, found := f.BootstrapFor("")
	if !found || legacy.Workspace.URLKey != "" {
		t.Fatal("legacy empty logical key source changed")
	}
	read, found, err := f.BeginProjectTemplatesRead(t.Context(), "")
	if err != nil || !found || !reflect.DeepEqual(read.Templates(), legacy.ProjectTemplates) {
		t.Fatal("logical empty key was substituted with the default registry key", found, err)
	}
	read.Close()
}

func TestScaleP21SW16ProjectTemplateLifecycleOracle(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projectTemplates"), workspace, "scale_p21_sw16.template_nil", "", nil, func(data *domain.Bootstrap) error {
		data.ProjectTemplates = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		return fields["projectTemplates"]
	}
	if !bytes.Equal(root(), []byte("null")) {
		t.Fatal("supported nil mutation did not persist a nullable root")
	}
	warm, ok := f.BootstrapFor(workspace)
	if !ok || warm.ProjectTemplates != nil {
		t.Fatal("local callback state lost its warm nil slice")
	}
	read, found, err := f.BeginProjectTemplatesRead(t.Context(), workspace)
	if err != nil || !found || !reflect.DeepEqual(read.Templates(), warm.ProjectTemplates) {
		t.Fatal("warm nil direct accessor differs", found, err)
	}
	read.Close()
	// ReloadWorkspace normally skips an exact copy. Force its existing
	// actual-decode helper so this checks the real lifecycle transition.
	reloadFromRows(t, f.SQLiteStore, workspace)
	cold, ok := f.BootstrapFor(workspace)
	if !ok || cold.ProjectTemplates == nil || len(cold.ProjectTemplates) != 0 {
		t.Fatal("actual reload did not normalize nullable templates to []")
	}
	read, found, err = f.BeginProjectTemplatesRead(t.Context(), "")
	if err != nil || !found || !reflect.DeepEqual(read.Templates(), cold.ProjectTemplates) {
		t.Fatal("actual reload/default installed accessor differs", found, err)
	}
	read.Close()
	if !bytes.Equal(root(), []byte("null")) {
		t.Fatal("reload unexpectedly rewrote the canonical nullable root")
	}
	t.Log("same canonical null root: local callback emits null; forced actual reload emits []")
}
