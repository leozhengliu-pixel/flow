package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestLoopContractSaveRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contracts.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := &server{store: repo}
	key := repo.Bootstrap().Workspace.URLKey
	loop := domain.Loop{ID: "loop_save", Name: "Summary", Instructions: "Reply with a summary."}
	hash := loopContractHashOf(loop)
	person := &domain.LoopOutputContract{Source: loopContractPerson, InstructionsHash: "older", Outputs: []domain.LoopContractOutput{{Kind: "document", Operation: loopOpAppend, Target: domain.LoopContractTarget{Mode: "existing", DocumentID: "chosen_document"}}}}
	if err := repo.MutateWorkspace(t.Context(), key, "loop.created", loop.ID, nil, func(data *domain.Bootstrap) error {
		data.Loops = append(data.Loops, loop)
		for i := 0; i < 4; i++ {
			version := domain.LoopVersion{ID: fmt.Sprintf("version_save_%d", i), LoopID: loop.ID, Definition: domain.LoopDefinition{Name: loop.Name, Instructions: loop.Instructions}}
			if i == 2 {
				version.Definition.Instructions = "Different instructions."
			}
			if i == 3 {
				version.OutputContract = person
			}
			data.LoopVersions = append(data.LoopVersions, version)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE contract_write_probe (table_name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"workspace_metadata_records", "workspace_states", "domain_events", "issue_records", "workspace_content_records"} {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER contract_probe_%s_%s AFTER %s ON %s BEGIN INSERT INTO contract_write_probe VALUES('%s'); END`, table, operation, operation, table, table)); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func() domain.Bootstrap {
		t.Helper()
		rows, err := db.Query(`SELECT field,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('loops','loopVersions') ORDER BY record_key`, key)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var data domain.Bootstrap
		for rows.Next() {
			var field string
			var raw []byte
			if err := rows.Scan(&field, &raw); err != nil {
				t.Fatal(err)
			}
			if field == "loops" {
				var item domain.Loop
				if err := json.Unmarshal(raw, &item); err != nil {
					t.Fatal(err)
				}
				data.Loops = append(data.Loops, item)
			} else {
				var item domain.LoopVersion
				if err := json.Unmarshal(raw, &item); err != nil {
					t.Fatal(err)
				}
				data.LoopVersions = append(data.LoopVersions, item)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return data
	}
	assertNoWrite := func(save func()) {
		t.Helper()
		if _, err := db.Exec(`DELETE FROM contract_write_probe`); err != nil {
			t.Fatal(err)
		}
		save()
		var written int
		if err := db.QueryRow(`SELECT COUNT(*) FROM contract_write_probe`).Scan(&written); err != nil || written != 0 {
			t.Fatalf("no-op wrote %d rows, want 0: %v", written, err)
		}
	}
	model := &domain.LoopOutputContract{Source: loopContractModel, InstructionsHash: hash, ReadOnly: true, Outputs: []domain.LoopContractOutput{}}
	srv.storeLoopContract(key, loop.ID, hash, model)
	data := read()
	if got := loopByID(&data, loop.ID).OutputContract; got == nil || got.Source != loopContractModel {
		t.Fatal("normal contract not persisted")
	}
	if len(data.LoopVersions) != 4 || data.LoopVersions[0].OutputContract == nil || data.LoopVersions[1].OutputContract == nil || data.LoopVersions[2].OutputContract != nil || data.LoopVersions[3].OutputContract.Source != loopContractPerson {
		t.Fatal("matching versions or person version protection changed")
	}
	assertNoWrite(func() { srv.storeLoopContract(key, loop.ID, hash, model) })
	fallback := loopFallbackContract(hash, "scripted extraction failure")
	srv.storeLoopContract(key, loop.ID, hash, fallback)
	data = read()
	if loopByID(&data, loop.ID).OutputContract.Source != loopContractFallback || data.LoopVersions[0].OutputContract.Source != loopContractModel {
		t.Fatal("fallback downgrade changed the published versions")
	}
	assertNoWrite(func() { srv.storeLoopContract(key, loop.ID, hash, fallback) })
	// Hold the model response while another replica changes the instructions.
	started, response := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(response) })
	provider := &fakeLoopProvider{contracts: []string{`tool:record_output_contract {"readOnly":true,"outputs":[],"always":false,"skipConditions":[]}`}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-response
		provider.serve(w, r)
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(release)
	srv.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "scripted-contract"}
	srv.agentClient, srv.loopContractLive = upstream.Client(), true
	completed := make(chan *domain.LoopOutputContract, 1)
	go func(loop domain.Loop) { completed <- srv.loopContractFor(t.Context(), key, loop) }(loop)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("extraction did not start")
	}
	loop.Instructions = "Newer instructions."
	loop.OutputContract = person
	loop.SuggestedOutputContract = model
	raw, _ := json.Marshal(loop)
	if _, err := db.Exec(`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='loops' AND record_key=?`, raw, key, loop.ID); err != nil {
		t.Fatal(err)
	}
	version := data.LoopVersions[1]
	version.OutputContract = nil
	versionRaw, _ := json.Marshal(version)
	if _, err := db.Exec(`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='loopVersions' AND record_key=?`, versionRaw, key, version.ID); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case model = <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("extraction did not finish")
	}
	data = read()
	if !reflect.DeepEqual(*loopByID(&data, loop.ID), loop) {
		t.Fatal("late extraction changed the Loop or its contracts")
	}
	modelRaw, _ := json.Marshal(model)
	savedRaw, _ := json.Marshal(data.LoopVersions[1].OutputContract)
	if model.Source != loopContractModel || !bytes.Equal(savedRaw, modelRaw) {
		t.Fatal("late extraction did not backfill the version with the old instructions")
	}
	if data.LoopVersions[2].OutputContract != nil || !reflect.DeepEqual(data.LoopVersions[3].OutputContract, person) {
		t.Fatal("late extraction changed a nonmatching or person-owned version")
	}
	assertNoWrite(func() { srv.storeLoopContract(key, loop.ID, hash, model) })
	assertNoWrite(func() { srv.storeLoopContract(key, loop.ID, hash, fallback) })
	newHash := loopContractHashOf(loop)
	suggestion := &domain.LoopOutputContract{Source: loopContractModel, InstructionsHash: newHash, Outputs: []domain.LoopContractOutput{{Kind: "document", Operation: loopOpAppend, Target: domain.LoopContractTarget{Mode: "existing", Ambiguous: true}}}}
	srv.storeLoopContract(key, loop.ID, newHash, suggestion)
	data = read()
	got := loopByID(&data, loop.ID)
	if got.OutputContract.Source != loopContractPerson || got.SuggestedOutputContract == nil || got.SuggestedOutputContract.Outputs[0].Target.DocumentID != "chosen_document" {
		t.Fatal("person contract or chosen target was lost")
	}
	assertNoWrite(func() { srv.storeLoopContract(key, loop.ID, newHash, suggestion) })
	// A reviewed, current person contract must not gain another suggestion.
	loop.OutputContract.InstructionsHash = newHash
	loop.SuggestedOutputContract = nil
	raw, _ = json.Marshal(loop)
	if _, err := db.Exec(`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='loops' AND record_key=?`, raw, key, loop.ID); err != nil {
		t.Fatal(err)
	}
	assertNoWrite(func() { srv.storeLoopContract(key, loop.ID, newHash, suggestion) })
}
