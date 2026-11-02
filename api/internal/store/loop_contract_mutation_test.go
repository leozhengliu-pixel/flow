package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
)

func TestLoopContractMutationUsesFreshNarrowRecords(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "contracts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := repo.Bootstrap().Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), key, "loop.created", "loop_contract", nil, func(data *domain.Bootstrap) error {
		data.Loops = append(data.Loops, domain.Loop{ID: "loop_contract", Instructions: "cached instructions"})
		data.LoopVersions = append(data.LoopVersions,
			domain.LoopVersion{ID: "version_contract", LoopID: "loop_contract"},
			domain.LoopVersion{ID: "version_other", LoopID: "loop_other"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Change only the database, leaving this replica's snapshot stale.
	raw, _ := json.Marshal(domain.Loop{ID: "loop_contract", Instructions: "fresh instructions"})
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='loops' AND record_key='loop_contract'`, raw, key); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	mutate := func(data *domain.Bootstrap) error {
		if len(data.Loops) != 1 || len(data.LoopVersions) != 1 || data.Loops[0].Instructions != "fresh instructions" {
			t.Fatalf("not fresh or not scoped: loops=%d versions=%d", len(data.Loops), len(data.LoopVersions))
		}
		if len(data.Issues)+len(data.Comments)+len(data.Activities)+len(data.Notifications)+len(data.Users)+len(data.Teams)+len(data.Documents) != 0 {
			t.Fatal("contract save loaded unrelated collections")
		}
		data.Loops[0].OutputContract = &domain.LoopOutputContract{Source: "model", ReadOnly: true}
		data.LoopVersions[0].OutputContract = data.Loops[0].OutputContract
		return nil
	}
	ctx, trace := startMutationTrace(t.Context())
	if err := repo.dispatchMutation(withMutationAggregateID(ctx, "loop_contract"), key, "loop.output_contract", nil, func(data *domain.Bootstrap) (string, error) {
		return "loop_contract", mutate(data)
	}); err != nil {
		t.Fatal(err)
	}
	if trace.path != "loop_contract" {
		t.Fatalf("contract save fell back to %q", trace.path)
	}
	changed := writes()
	if changed["workspace_metadata_records"] != 2 || len(changed) != 1 {
		t.Fatalf("unexpected writes: %v", changed)
	}
	var eventsBefore, eventsAfter int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events`).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(t.Context(), key, "loop.output_contract", "loop_contract", nil, mutate); err != nil {
		t.Fatal(err)
	}
	if changed := writes(); len(changed) != 0 {
		t.Fatalf("identical contract wrote rows: %v", changed)
	}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events`).Scan(&eventsAfter); err != nil || eventsAfter != eventsBefore {
		t.Fatalf("identical contract wrote event: before=%d after=%d err=%v", eventsBefore, eventsAfter, err)
	}
}
