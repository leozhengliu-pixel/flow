package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP04VerificationDetectsOmittedReadsAndWrites(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprint(write), func(t *testing.T) {
			repo := scaleP04Store(t)
			stored, _ := repo.storedWorkspace("test-workspace")
			stored.Settings = map[string]any{"p04Required": "required value"}
			input := metadataFieldSnapshot(stored, []string{"users"})
			input.Issues = []domain.Issue{{ID: "p04-guard"}}
			tx, err := repo.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			defer func() {
				value := recover()
				if value == nil || !strings.Contains(fmt.Sprint(value), "P04 mutation verification mismatch") {
					t.Fatalf("omitted input did not fail loudly: %v", value)
				}
			}()
			_, _ = repo.compareMutationCallback(t.Context(), tx, "guard test", stored, &input, []string{"users"}, nil, func(data *domain.Bootstrap) (string, error) {
				if write {
					data.Documents = append(data.Documents, domain.Document{ID: "p04-new"})
				} else {
					data.Issues[0].Title, _ = data.Settings["p04Required"].(string)
				}
				return "p04-guard", nil
			}, true, false)
		})
	}
}

func TestScaleP04VerificationProductionFallback(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	repo := scaleP04Store(t)
	stored, _ := repo.storedWorkspace("test-workspace")
	stored.Settings = map[string]any{"p04Required": "full result"}
	input := metadataFieldSnapshot(stored, []string{"users"})
	input.Issues = []domain.Issue{{ID: "p04-guard"}}
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var returned string
	id, err := repo.compareMutationCallback(t.Context(), tx, "guard test", stored, &input, []string{"users"}, nil, func(data *domain.Bootstrap) (string, error) {
		data.Issues[0].Title, _ = data.Settings["p04Required"].(string)
		returned = data.Issues[0].Title
		return "p04-guard", nil
	}, false, false)
	if err != nil || id != "p04-guard" || input.Issues[0].Title != "full result" || returned != "full result" {
		t.Fatalf("fallback did not use full records/response capture: %s %v %+v %q", id, err, input.Issues, returned)
	}
}

func TestScaleP04VerificationDetectsResponseOnlyDifferences(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	repo := scaleP04Store(t)
	stored, _ := repo.storedWorkspace("test-workspace")
	stored.Settings = map[string]any{"required": "response metadata"}
	input := metadataFieldSnapshot(stored, []string{"users"})
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var response string
	ctx := ContextWithMutationResults(t.Context(), &response)
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "callback.results") {
			t.Fatalf("response-only difference was not detected: %v", value)
		}
	}()
	_, _ = repo.compareMutationCallback(ctx, tx, "guard test", stored, &input, []string{"users"}, nil, func(data *domain.Bootstrap) (string, error) {
		response, _ = data.Settings["required"].(string)
		return "response", nil
	}, true, false)
}

func TestScaleP04VerificationPreservesFieldsAndIdentities(t *testing.T) {
	stamp := idgen.Stamp()
	protected, _ := json.Marshal(domain.Bootstrap{Settings: map[string]any{"existing": stamp}})
	makeResult := func() mutationCallbackResult {
		first, second := idgen.Stamp(), idgen.Stamp()
		return mutationCallbackResult{Data: domain.Bootstrap{
			Comments: map[string][]domain.Comment{"issue": {{ID: "comment_" + first, Body: "first", CreatedAt: time.Now().UTC()}, {ID: "comment_" + second, Body: "second", CreatedAt: time.Now().UTC()}}},
			Settings: map[string]any{"existing": stamp, "first": "comment_" + first, "second": "comment_" + second},
		}}
	}
	first, second := makeResult(), makeResult()
	if path := compareMutationCallbacks(first, second, protected); path != "" {
		t.Fatalf("new clocks/ids did not normalize: %s", path)
	}
	second.Data.Comments["issue"][1].Body = "changed"
	if compareMutationCallbacks(first, second, protected) == "" {
		t.Fatal("changed body was ignored")
	}
	second = makeResult()
	second.Data.Settings["existing"] = idgen.Stamp()
	if compareMutationCallbacks(first, second, protected) == "" {
		t.Fatal("existing identifier was normalized away")
	}
	second = makeResult()
	second.Data.Comments["issue"] = second.Data.Comments["issue"][:1]
	if compareMutationCallbacks(first, second, protected) == "" {
		t.Fatal("record cardinality was ignored")
	}
	second = makeResult()
	second.ErrorType, second.Error = "error", "changed error"
	if compareMutationCallbacks(first, second, protected) == "" {
		t.Fatal("callback error was ignored")
	}
	second = makeResult()
	second.Audit = []domain.AuditLogEntry{{ID: "extra", Action: "extra"}}
	if compareMutationCallbacks(first, second, protected) == "" {
		t.Fatal("audit intent was ignored")
	}
}
