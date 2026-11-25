package store

import (
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21IssueCallbackLookupBridge(t *testing.T) {
	owner := &SQLiteStore{}
	tx := &sqlTx{}
	formerCalls, updateCalls, callbacks := 0, 0, 0
	ctx := withMutationCallbackLookups(t.Context(), owner, tx, func() map[string]bool {
		formerCalls++
		return map[string]bool{"departed": true}
	}, func(id string) ([][]byte, error) {
		updateCalls++
		if id != "document" {
			t.Fatal("lookup identity changed", id)
		}
		return [][]byte{[]byte("first"), []byte("last")}, nil
	})
	stored := domain.Bootstrap{Workspace: domain.Workspace{ID: "workspace"}, Settings: map[string]any{"original": "value"}}
	data := cloneBootstrap(stored)
	aggregate, err := owner.compareMutationCallback(ctx, tx, "issue", stored, &data, []string{"settings"}, nil, func(input *domain.Bootstrap) (string, error) {
		callbacks++
		if !input.FormerMembers()["departed"] {
			t.Fatal("owned former-member service was replaced")
		}
		updates, err := input.CollaborationUpdates("document")
		if err != nil || !reflect.DeepEqual(updates, [][]byte{[]byte("first"), []byte("last")}) {
			t.Fatal("owned collaboration service was replaced", updates, err)
		}
		input.Settings["result"] = string(updates[0])
		return "aggregate", nil
	}, true, false)
	if err != nil || aggregate != "aggregate" || callbacks != 2 || formerCalls != 2 || updateCalls != 2 {
		t.Fatal("verification lost owned services or independent callbacks", aggregate, err, callbacks, formerCalls, updateCalls)
	}
	if stored.Settings["result"] != nil || data.Settings["result"] != "first" {
		t.Fatal("callback clone isolation changed", stored.Settings, data.Settings)
	}
	for _, invalid := range []struct {
		name  string
		owner *SQLiteStore
		tx    *sqlTx
	}{
		{"foreign-owner", &SQLiteStore{}, tx},
		{"missing-owner", nil, tx},
		{"foreign-transaction", owner, &sqlTx{}},
		{"missing-transaction", owner, nil},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			called := false
			_, err := invalid.owner.compareMutationCallback(ctx, invalid.tx, "issue", stored, &data, nil, nil, func(*domain.Bootstrap) (string, error) {
				called = true
				return "", nil
			}, true, false)
			if !errors.Is(err, ErrMetadataRecordQuery) || called {
				t.Fatal("foreign callback service crossed its transaction", err, called)
			}
		})
	}
	if lookups, err := mutationCallbackLookupsFor(ctx, &SQLiteStore{}, nil, "metadata"); err != nil || lookups != nil {
		t.Fatal("issue override changed another callback family's preparation", lookups, err)
	}
	if lookups, err := mutationCallbackLookupsFor(t.Context(), owner, tx, "issue"); err != nil || lookups != nil {
		t.Fatal("literal callback path acquired an override", lookups, err)
	}
}
