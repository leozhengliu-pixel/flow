package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP16OracleResetsCapturesAndKeepsPayloadAliases(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "on")
	data := domain.Bootstrap{Projects: []domain.Project{{ID: "p", Name: "before"}, {ID: "other", Name: "unrelated"}}, AuditLog: []domain.AuditLogEntry{{ID: "historical"}}}
	payload := struct{ Child *struct{ ID string } }{Child: &struct{ ID string }{}}
	alias := payload.Child
	keys := []string{"existing"}
	ctx := WithMetadataRecords(context.Background(), MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "p"}}})
	ctx = WithMetadataRecordVerification(ctx, &payload, &keys)
	callback := verifiedMetadataRecordMutationAdapter(ctx, "project.updated", payload, func(input *domain.Bootstrap) (string, error) {
		payload.Child.ID = idgen.New("child_")
		input.Projects[0].Description = payload.Child.ID
		keys = append(keys, "object-to-delete")
		return "p", nil
	})
	if _, err := callback(&data); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || payload.Child != alias || alias.ID != data.Projects[0].Description {
		t.Fatalf("oracle duplicated captures or broke the persisted child alias: keys=%v", keys)
	}
	if len(data.Projects) != 2 || data.Projects[1].Name != "unrelated" {
		t.Fatal("oracle lost an unloaded record")
	}
	if len(data.AuditLog) != 1 || data.AuditLog[0].ID != "historical" {
		t.Fatal("oracle changed the collector's historical input")
	}
}

func TestScaleP16OracleDetectsMissingInputsAndPreviousValues(t *testing.T) {
	before := domain.Bootstrap{Projects: []domain.Project{{ID: "p"}, {ID: "dependency", Name: "must be present"}}}
	ctx := WithMetadataRecords(context.Background(), MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "p"}}})
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	callback := func(data *domain.Bootstrap) (string, error) {
		for _, project := range data.Projects {
			if project.ID == "dependency" {
				data.Projects[0].Description = project.Name
			}
		}
		return "p", nil
	}
	narrow, full := cloneCallbackInput(before), cloneCallbackInput(before)
	a, _, _ := runVerifiedCallback(&narrow, nil, nil, nil, metadataRecordMutationAdapter(ctx, callback))
	b, _, _ := runVerifiedCallback(&full, nil, nil, nil, callback)
	protected, _ := json.Marshal(before)
	if path := compareMetadataRecordCallbacks(a, b, protected); !strings.Contains(path, "description") {
		t.Fatalf("missing dependency escaped the oracle: %q", path)
	}
	left, right := a, a
	left.Payload, right.Payload = json.RawMessage(`{"previous":"old"}`), json.RawMessage(`{"previous":"wrong"}`)
	if compareMetadataRecordCallbacks(left, right, protected) == "" {
		t.Fatal("previous-value difference escaped the oracle")
	}
}

func TestScaleP16RecordInputOwnershipAtTwentyThousandProjects(t *testing.T) {
	data := scaleP16RecordFixture()
	selected := metadataRecordSnapshot(data, MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "target"}}}, []string{"projects"})
	if len(selected.Projects) != 1 {
		t.Fatalf("loaded %d projects", len(selected.Projects))
	}
	selected.Projects[0].Comments[0].Reactions["+1"][0] = "changed"
	if !reflect.DeepEqual(data.Projects[0].Comments[0].Reactions["+1"], []string{"viewer"}) {
		t.Fatal("callback changed the resident record")
	}
}

func TestScaleP16OracleNormalizesGeneratedSlugsOnly(t *testing.T) {
	first := mutationCallbackResult{Data: domain.Bootstrap{Projects: []domain.Project{{ID: "p", SlugID: "new-123456789012"}}}}
	second := mutationCallbackResult{Data: domain.Bootstrap{Projects: []domain.Project{{ID: "p", SlugID: "new-abcdef123456"}}}}
	if path := compareMetadataRecordCallbacks(first, second, nil); path != "" {
		t.Fatalf("new slug suffixes differ at %s", path)
	}
	protected, _ := json.Marshal(first.Data)
	if compareMetadataRecordCallbacks(first, second, protected) == "" {
		t.Fatal("changed existing slug escaped the oracle")
	}
}

func TestScaleP16RecordInputsPreserveOwnedAttribution(t *testing.T) {
	for _, mode := range []string{"off", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
			data := domain.Bootstrap{Viewer: domain.User{ID: "viewer", Via: &domain.ActorBot{ID: "agent", Type: "agent", OnBehalfOfID: "viewer", OnBehalfOfName: "Original"}}}
			selected := metadataRecordSnapshot(data, MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "target"}}}, []string{"projects"})
			if selected.Viewer.Via == data.Viewer.Via || !reflect.DeepEqual(selected.Viewer.Via, data.Viewer.Via) {
				t.Fatal("record callback lost attribution or shares the request's actor pointer")
			}
			selected.Viewer.Via.OnBehalfOfName = "Changed"
			if data.Viewer.Via.OnBehalfOfName != "Original" {
				t.Fatal("record callback changed request attribution")
			}
		})
	}
}

func TestScaleP16RecordReloadScopeIsolation(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	fixture := repo.Bootstrap()
	workspace, target := fixture.Workspace.URLKey, fixture.Projects[0].ID
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p16.reload_seed", "", nil, func(data *domain.Bootstrap) error {
		for range 70 {
			data.Projects = append(data.Projects, domain.Project{ID: idgen.New("p16-reload-"), Description: "Keep this record"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The legacy reload normalizes stored display defaults. Establish that
	// full reload as the oracle before comparing the scoped reloads.
	repo.mu.Lock()
	repo.forgetCopyRevisionLocked(workspace)
	repo.mu.Unlock()
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.WorkspaceMetadata(workspace)
	for _, scope := range []MetadataRecordScope{{Write: []RecordKey{{Kind: "projects", ID: target}}}, {Fields: []string{"projects"}}} {
		ctx := WithMetadataRecords(t.Context(), scope)
		repo.mu.Lock()
		repo.forgetCopyRevisionLocked(workspace)
		repo.mu.Unlock()
		if err := repo.ReloadWorkspace(ctx, workspace); err != nil {
			t.Fatal(err)
		}
		after, _ := repo.WorkspaceMetadata(workspace)
		if !reflect.DeepEqual(before.Projects, after.Projects) || !reflect.DeepEqual(before.Documents, after.Documents) || !reflect.DeepEqual(before.Users, after.Users) {
			t.Fatal("canonical reload installed a partial record selection")
		}
	}
	hydrated, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{Kind: "projects", ID: target}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated.Data.Projects) != 1 || hydrated.Data.Projects[0].ID != target {
		t.Fatal("explicit hydration lost its bounded selection")
	}
}

func scaleP16RecordFixture() domain.Bootstrap {
	data := domain.Bootstrap{Projects: make([]domain.Project, 20000)}
	for i := range data.Projects {
		data.Projects[i] = domain.Project{ID: "p16-" + strconv.Itoa(i), Description: strings.Repeat("body", 512), Comments: []domain.Comment{{Body: "thread", Reactions: map[string][]string{"+1": {"viewer"}}}}}
	}
	data.Projects[0].ID = "target"
	return data
}

func BenchmarkScaleP16RecordInputs(b *testing.B) {
	data := scaleP16RecordFixture()
	for _, mode := range []string{"full", "record"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if mode == "full" {
					metadataFieldSnapshot(data, []string{"projects"})
				} else {
					metadataRecordSnapshot(data, MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "target"}}}, []string{"projects"})
				}
			}
		})
	}
}
