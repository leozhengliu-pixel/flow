package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW554: a COLD legacy write that panics does not deadlock later requests.
// The panic is raised inside the write's install, while the writer holds the
// facade's lock and the live store's write lock: the write lock is returned
// on unwinding, the facade is dropped without waiting for its lock, the COLD
// slot is released, the live write lock is free, and the next COLD write and a legacy read
// complete. A panic in the callback itself is covered the same way.
func TestScaleP21SW554PanickingColdWriteReleasesItsLocks(t *testing.T) {
	_, view, workspace := scaleP21ColdReadFixture(t)
	view.metadataResidency.work.limit = metadataWorkBytes
	ctx := WithColdWork(t.Context(), "sw554")
	write := func(id string) error {
		return view.MutateWorkspace(ctx, workspace, "scale_p21_sw554.label", "", nil, func(data *domain.Bootstrap) error {
			if id == "callback-panic" {
				panic("sw554 callback")
			}
			data.Labels = append(data.Labels, domain.IssueLabel{ID: id, Name: id})
			return nil
		})
	}
	panics := func(run func()) (panicked bool) {
		defer func() { panicked = recover() != nil }()
		run()
		return false
	}
	SetColdInstallHookForTesting(func() { panic("sw554 install") })
	panicked := panics(func() { _ = write("install-panic") })
	SetColdInstallHookForTesting(nil)
	if !panicked {
		t.Fatal("SW554 the install hook did not panic")
	}
	if !panics(func() { _ = write("callback-panic") }) {
		t.Fatal("SW554 the callback did not panic")
	}
	done := make(chan error, 1)
	go func() {
		if held := ColdSlotsHeldForTesting(); held != 0 {
			done <- context.DeadlineExceeded
			return
		}
		if !view.writeMu.TryLock() {
			done <- context.DeadlineExceeded
			return
		}
		view.writeMu.Unlock()
		done <- write("after-panics")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("SW554 after the panics", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SW554 a COLD write after a panicking one deadlocked")
	}
	data, ok := view.WorkspaceMetadataFields(workspace, "labels")
	if !ok || !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == "after-panics" }) {
		t.Fatal("SW554 the write after the panics is missing from the bounded image")
	}
	// The install-panic write committed before its install: the parent was
	// reloaded when the write lock was returned, so SQL and the image agree.
	if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == "install-panic" }) {
		t.Fatal("SW554 the committed write is missing from the bounded image after its install panicked")
	}
	if slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == "callback-panic" }) {
		t.Fatal("SW554 the panicking callback's write was published")
	}
}
