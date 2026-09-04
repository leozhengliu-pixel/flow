package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestPasskeyMetadataLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	owner := bootstrap.Viewer.ID
	err = repository.MutateWorkspace(t.Context(), "test-workspace", "passkey.test_seed", "pk-test", nil, func(data *domain.Bootstrap) error {
		data.Passkeys = []domain.Passkey{{ID: "pk-test", UserID: owner, Name: "MacBook", CredentialJSON: `{"private":"never-return"}`, CreatedAt: time.Now().UTC()}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items := requestJSON[[]domain.Passkey](t, handler, http.MethodGet, "/api/account/passkeys", nil, http.StatusOK)
	if len(items) != 1 || items[0].Name != "MacBook" || items[0].CredentialJSON != "" {
		t.Fatalf("unexpected passkey list: %#v", items)
	}
	updated := requestJSON[domain.Passkey](t, handler, http.MethodPatch, "/api/account/passkeys/pk-test", map[string]string{"name": "Work Mac"}, http.StatusOK)
	if updated.Name != "Work Mac" || updated.CredentialJSON != "" {
		t.Fatalf("unexpected passkey update: %#v", updated)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/account/passkeys/pk-test", nil, http.StatusNoContent)
	items = requestJSON[[]domain.Passkey](t, handler, http.MethodGet, "/api/account/passkeys", nil, http.StatusOK)
	if len(items) != 0 {
		t.Fatalf("passkey was not deleted: %#v", items)
	}
}
