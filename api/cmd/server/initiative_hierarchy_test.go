package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
)

func TestInitiativeHierarchyMutationContracts(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	create := func(name string, parents ...string) domain.Initiative {
		return requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": name, "parentInitiativeIds": parents}, http.StatusCreated)
	}
	a, b := create("Parent A"), create("Parent B")
	child := create("Child", a.ID, b.ID, a.ID)
	if len(child.ParentInitiativeIDs) != 2 {
		t.Fatalf("Expected deduplicated multi-parent relation: %#v", child.ParentInitiativeIDs)
	}
	leaf := create("Leaf", child.ID)
	requestJSON[any](t, handler, http.MethodPatch, "/api/initiatives/"+a.ID, map[string]any{"parentInitiativeIds": []string{leaf.ID}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/initiatives/"+a.ID+"/relations", map[string]any{"type": "parent", "relatedInitiativeId": child.ID}, http.StatusBadRequest)
	related := requestJSON[domain.InitiativeRelation](t, handler, http.MethodPost, "/api/initiatives/"+a.ID+"/relations", map[string]any{"type": "related", "relatedInitiativeId": child.ID}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, "/api/initiatives/"+a.ID+"/relations/"+related.ID, map[string]any{"type": "parent"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+a.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.Initiatives, func(item domain.Initiative) bool { return item.ID == child.ID })
	if index < 0 || !slices.Equal(bootstrap.Initiatives[index].ParentInitiativeIDs, []string{b.ID}) {
		t.Fatal("Deleting one parent must preserve child and other parent")
	}
	if slices.ContainsFunc(bootstrap.InitiativeRelations, func(r domain.InitiativeRelation) bool { return r.InitiativeID == a.ID || r.RelatedInitiativeID == a.ID }) {
		t.Fatal("Deleted parent retained dangling relations")
	}
	newParent := create("Parent C")
	edge := requestJSON[domain.InitiativeRelation](t, handler, http.MethodPost, "/api/initiatives/"+child.ID+"/relations", map[string]any{"type": "parent", "relatedInitiativeId": newParent.ID}, http.StatusCreated)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, item := range bootstrap.Initiatives {
		if item.ID == child.ID && !slices.Contains(item.ParentInitiativeIDs, newParent.ID) {
			t.Fatal("Relation API did not synchronize parent IDs")
		}
	}
	requestJSON[domain.InitiativeRelation](t, handler, http.MethodPatch, "/api/initiatives/"+child.ID+"/relations/"+edge.ID, map[string]any{"type": "related"}, http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, item := range bootstrap.Initiatives {
		if item.ID == child.ID && slices.Contains(item.ParentInitiativeIDs, newParent.ID) {
			t.Fatal("Changing relation type retained parent")
		}
	}
}

func TestInitiativeNestingLimitMatchesLinear(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	create := func(name string, status int, parents ...string) domain.Initiative {
		return requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": name, "parentInitiativeIds": parents}, status)
	}
	chain := []domain.Initiative{create("Level 1", http.StatusCreated)}
	for level := 2; level <= domain.MaxInitiativeNesting; level++ {
		chain = append(chain, create("Level", http.StatusCreated, chain[len(chain)-1].ID))
	}
	create("Too deep", http.StatusBadRequest, chain[4].ID)

	tree := create("Tree", http.StatusCreated)
	create("Tree child", http.StatusCreated, tree.ID)
	// Level 1 › 2 › 3 › Tree › Tree child is five levels; under level 4 it would be six.
	requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+tree.ID, map[string]any{"parentInitiativeIds": []string{chain[2].ID}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/initiatives/"+tree.ID, map[string]any{"parentInitiativeIds": []string{chain[3].ID}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/initiatives/"+tree.ID+"/relations", map[string]any{"type": "parent", "relatedInitiativeId": chain[3].ID}, http.StatusBadRequest)
	// Keeping an existing parent while editing other fields stays allowed.
	requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+tree.ID, map[string]any{"parentInitiativeIds": []string{chain[2].ID}, "name": "Tree renamed"}, http.StatusOK)
}

func TestInitiativeNestingTooDeepCountsBothSides(t *testing.T) {
	parents := map[string][]string{"l2": {"l1"}, "l3": {"l2"}, "l4": {"l3"}, "child": {"tree"}, "loop-a": {"loop-b"}, "loop-b": {"loop-a"}}
	if domain.InitiativeNestingTooDeep(parents, "tree", []string{"l3"}) {
		t.Fatal("a two-level subtree fits under the third level")
	}
	if !domain.InitiativeNestingTooDeep(parents, "tree", []string{"l4"}) {
		t.Fatal("a two-level subtree under the fourth level makes six levels")
	}
	if domain.InitiativeNestingTooDeep(parents, "solo", []string{"loop-a"}) {
		t.Fatal("legacy cycles must not loop or overcount")
	}
}
