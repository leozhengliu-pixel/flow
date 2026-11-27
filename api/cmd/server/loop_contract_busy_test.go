package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// With bounded metadata the loop output-contract store is COLD work. While
// the COLD slot stays taken past its wait, a starting run still gets the read
// contract at once (one model read for the loop text), the editor's read waits
// for the store, and the store retries until the slot frees instead of losing
// the contract.
func TestScaleLoopContractStoreWaitsOutBusyColdSlot(t *testing.T) {
	f, workspace := routeTemperatureGuardFixture(t)
	view := f.probe
	work := store.WithColdWork(context.Background(), "loop-contract-busy")
	loop := domain.Loop{ID: "busy-contract-loop", Name: "Busy loop", Instructions: "Summarize the open bugs of the busy workspace", Status: "active", TriggerType: "manual", ConnectorIDs: []string{}}
	if err := view.MutateWorkspace(work, workspace, "scale_p21.loop_contract_busy", "", nil, func(data *domain.Bootstrap) error {
		data.Loops = append(data.Loops, loop)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	previousStub, previousBackoff := loopContractStub, loopContractStoreBackoff
	loopContractStub = func(domain.Loop) (*domain.LoopOutputContract, error) {
		reads.Add(1)
		return &domain.LoopOutputContract{ReadOnly: true, Outputs: []domain.LoopContractOutput{}}, nil
	}
	loopContractStoreBackoff = []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond,
		50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}
	view.SetColdQueueForTesting(64, 30*time.Millisecond)
	t.Cleanup(func() {
		loopContractStub, loopContractStoreBackoff = previousStub, previousBackoff
		view.SetColdQueueForTesting(64, 15*time.Second)
	})
	s := &server{store: view, agent: appconfig.AgentConfig{Enabled: true, Model: "scripted-contract"}}

	// A COLD legacy write holds the COLD slot.
	release, holding, held := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		held <- view.MutateWorkspace(work, workspace, "scale_p21.loop_contract_hold", "", nil, func(*domain.Bootstrap) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	released := false
	defer func() {
		if !released {
			close(release)
			<-held
		}
	}()

	started := time.Now()
	contract := s.loopRunContract(t.Context(), workspace, loop)
	if contract == nil || contract.Source != loopContractModel || contract.InstructionsHash != loopContractHashOf(loop) {
		t.Fatalf("a starting run got %+v", contract)
	}
	if again := s.loopRunContract(t.Context(), workspace, loop); again != contract || reads.Load() != 1 {
		t.Fatalf("a second run while the store waits read the model again (%d reads)", reads.Load())
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Fatalf("a starting run waited %v for the contract store", waited)
	}
	editor := make(chan *domain.LoopOutputContract, 1)
	go func() { editor <- s.loopContractFor(t.Context(), workspace, loop) }()
	// Several store attempts find the slot busy (each waits 30ms).
	select {
	case <-editor:
		t.Fatal("the editor's read returned before the contract was stored")
	case <-time.After(250 * time.Millisecond):
	}
	released = true
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-editor:
		if got != contract {
			t.Fatal("the editor's read got another contract")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the contract store did not finish after the COLD slot freed")
	}
	// The legacy owner reads the stored loop back from SQL.
	if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	data, ok := f.repository.WorkspaceMetadataFields(workspace, "loops")
	if !ok || loopByID(&data, loop.ID) == nil {
		t.Fatal("the loop is missing")
	}
	stored := *loopByID(&data, loop.ID)
	want, _ := json.Marshal(contract)
	got, _ := json.Marshal(stored.OutputContract)
	if string(got) != string(want) {
		t.Fatalf("stored contract %s, want %s", got, want)
	}
	if reads.Load() != 1 {
		t.Fatalf("%d model reads, want 1", reads.Load())
	}
}
