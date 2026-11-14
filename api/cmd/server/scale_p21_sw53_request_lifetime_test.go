package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW53Writer struct {
	*httptest.ResponseRecorder
	before func()
}

func (w *scaleP21SW53Writer) WriteHeader(status int) {
	if w.before != nil {
		w.before()
	}
	w.ResponseRecorder.WriteHeader(status)
}

func (w *scaleP21SW53Writer) Write(body []byte) (int, error) {
	if w.before != nil {
		w.before()
	}
	return w.ResponseRecorder.Write(body)
}

func TestScaleP21SW53HTTPResponseKeepsResultCharge(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		read, found, err := probe.BeginInstalledMetadataRead(t.Context(), f.data.Workspace.URLKey)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		defer read.Close()
		var held []*store.OwnedMetadataSelection
		for {
			selection, err := read.Select(t.Context(), store.MetadataRecordScope{})
			if errors.Is(err, store.ErrMetadataWorkBudget) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			held = append(held, selection)
			if len(held) > 256 {
				t.Fatal("failed to saturate owned selection work")
			}
		}
		if len(held) < 2 {
			t.Fatal("insufficient admission fixture")
		}
		held[len(held)-1].Close()
		var result *store.MetadataResultSlot
		service := &server{store: probe}
		handler := service.withMetadataLifetime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lifetime, err := probe.MetadataLifetime(r.Context())
			if err != nil {
				t.Fatal(err)
			}
			result, err = lifetime.NewResultSlot(r.Context(), 16<<20)
			if err != nil {
				t.Fatal(err)
			}
			if err := result.Capture(r.Context(), map[string]string{"value": "owned through HTTP encode"}); err != nil {
				t.Fatal(err)
			}
			respondMutation(w, nil, http.StatusOK, result.Data)
		}))
		writes := 0
		w := &scaleP21SW53Writer{ResponseRecorder: httptest.NewRecorder(), before: func() {
			writes++
			extra, err := read.Select(t.Context(), store.MetadataRecordScope{})
			if extra != nil {
				extra.Close()
			}
			if !errors.Is(err, store.ErrMetadataWorkBudget) {
				t.Error("response owner released before WriteHeader/Write", err)
			}
		}}
		r := httptest.NewRequest(http.MethodGet, "/api/sw53-test", nil)
		parent := r.Context()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || writes == 0 || !strings.Contains(w.Body.String(), "owned through HTTP encode") || r.Context() != parent || result.Data != nil {
			t.Fatal("response/parent/cleanup lifetime changed", w.Code, writes, w.Body.String())
		}
		extra, err := read.Select(t.Context(), store.MetadataRecordScope{})
		if err != nil {
			t.Fatal("HTTP lifetime leaked result work", err)
		}
		extra.Close()
	}
}

func TestScaleP21SW53HTTPErrorPanicAndExplicitBatch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: probe}
		for _, mode := range []string{"error", "panic"} {
			var slot *store.MetadataResultSlot
			handler := service.withMetadataLifetime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lifetime, err := probe.MetadataLifetime(r.Context())
				if err != nil {
					t.Fatal(err)
				}
				slot, err = lifetime.NewResultSlot(r.Context(), 64<<10)
				if err != nil {
					t.Fatal(err)
				}
				if err := slot.Capture(r.Context(), "captured"); err != nil {
					t.Fatal(err)
				}
				if mode == "panic" {
					panic("handler marker")
				}
				respondMutation(w, errors.New("fixture error"), http.StatusOK, slot.Data)
			}))
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/sw53-test", nil))
			}()
			if slot == nil || slot.Data != nil || mode == "panic" && recovered != "handler marker" || mode == "error" && recovered != nil {
				t.Fatal("error/panic skipped result cleanup", mode, recovered)
			}
		}
		for range 80 {
			ctx, batch, err := probe.BeginMetadataLifetime(t.Context())
			if err != nil {
				t.Fatal("explicit finite batches accumulated work", err)
			}
			slot, err := batch.NewResultSlot(ctx, 4<<10)
			if err != nil {
				batch.Close()
				t.Fatal(err)
			}
			if err := slot.Capture(ctx, "batch"); err != nil {
				t.Fatal(err)
			}
			batch.Close()
			if slot.Data != nil {
				t.Fatal("batch close retained data")
			}
		}
	}
}

func TestScaleP21SW53OwnedHandoffWinsOnceAndBindsIdentity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: probe}
		for _, issueWrite := range []bool{false, true} {
			r := httptest.NewRequest(http.MethodGet, "/api/imports", nil)
			data := domain.Bootstrap{Workspace: f.data.Workspace, ImportJobs: []domain.ImportJob{{ID: "earlier-snapshot", Filename: "warm\x00name", Headers: []string{}, Errors: nil}}}
			handOff := handOffPagedMetadata
			take := service.takeOwnedPagedMetadata
			if issueWrite {
				handOff, take = handOffIssueWriteMetadata, service.takeOwnedIssueWriteMetadata
			}
			handOff(r, f.data.Workspace.URLKey, f.data.Viewer.ID, data)
			freshReads := 0
			handler := service.withMetadataLifetime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, mismatch := range [][2]string{{"other-workspace", f.data.Viewer.ID}, {f.data.Workspace.URLKey, "other-user"}} {
					if _, found, err := take(r, mismatch[0], mismatch[1]); err != nil || found {
						t.Fatal("mismatch consumed another identity's handoff", found, err)
					}
				}
				earlier, found, err := take(r, f.data.Workspace.URLKey, f.data.Viewer.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !found {
					freshReads++
				}
				if len(earlier.ImportJobs) != 1 || earlier.ImportJobs[0].Filename != "warm\x00name" || earlier.ImportJobs[0].Headers == nil || earlier.ImportJobs[0].Errors != nil {
					t.Fatal("handoff source/concrete nil shape was replaced")
				}
				if _, found, err := take(r, f.data.Workspace.URLKey, f.data.Viewer.ID); err != nil || found {
					t.Fatal("handoff was consumed twice", found, err)
				}
				writeJSON(w, http.StatusOK, earlier.ImportJobs)
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if freshReads != 0 || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "earlier-snapshot") {
				t.Fatal("current state won over one-shot earlier handoff", freshReads, w.Code)
			}
		}
	}
}

func TestScaleP21SW53HandoffOwnershipFailureNeverFallsBack(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		ctx, lifetime, err := probe.BeginMetadataLifetime(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: probe}
		r := httptest.NewRequest(http.MethodGet, "/api/imports", nil).WithContext(ctx)
		handOffPagedMetadata(r, f.data.Workspace.URLKey, f.data.Viewer.ID, domain.Bootstrap{
			Workspace: f.data.Workspace, Settings: map[string]any{"oversized": strings.Repeat("x", 12<<20)},
		})
		if _, found, err := service.takeOwnedPagedMetadata(r, f.data.Workspace.URLKey, f.data.Viewer.ID); !found || !errors.Is(err, store.ErrMetadataWorkBudget) {
			t.Fatal("ownership error allowed fallback/current source", found, err)
		}
		if _, found := takePagedMetadata(r, f.data.Workspace.URLKey, f.data.Viewer.ID); found {
			t.Fatal("failed owned take duplicated one-shot handoff")
		}
		lifetime.Close()
	}
}

func TestScaleP21SW53OffMiddlewareAndHandoffRemainLiteral(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		service := &server{store: repo}
		original := http.NewServeMux()
		if service.withMetadataLifetime(original) != original {
			t.Fatal("off middleware wrapped the original handler")
		}
		parent, cancel := context.WithCancel(t.Context())
		cancel()
		r := httptest.NewRequest(http.MethodGet, "/api/imports", nil).WithContext(parent)
		handOffPagedMetadata(r, f.data.Workspace.URLKey, f.data.Viewer.ID, domain.Bootstrap{ImportJobs: []domain.ImportJob{{ID: "off-handoff"}}})
		literal := r.Context()
		called := false
		handler := service.withMetadataLifetime(http.HandlerFunc(func(w http.ResponseWriter, got *http.Request) {
			called = true
			if got.Context() != literal {
				t.Fatal("off middleware changed literal context")
			}
			data, found, err := service.takeOwnedPagedMetadata(got, f.data.Workspace.URLKey, f.data.Viewer.ID)
			if err != nil || !found || data.ImportJobs[0].ID != "off-handoff" {
				t.Fatal("off handoff added bounded cancellation/admission", found, err)
			}
		}))
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if !called {
			t.Fatal("off middleware intercepted the original handler")
		}
	}
}
