package main

import (
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestActivitySchemaDescribesLabelArraysAndUpdates(t *testing.T) {
	g := newGenerator()
	ref := g.ref(reflect.TypeOf(domain.ActivityEvent{}), false)
	schemas := toJSON(t, g.schemas).(map[string]any)
	value := map[string]any{
		"id": "event", "type": "issue.updated", "actor": toJSON(t, domain.User{ID: "actor"}),
		"createdAt": "2026-11-10T12:00:00Z", "updatedAt": "2026-11-10T12:05:00Z",
		"metadata": map[string]any{"addedLabelIds": []any{"bug"}, "removedLabelIds": []any{}, "labels": "bug", "priorityBefore": "Low", "priority": "High"},
	}
	if problems := validateSchema(schemas, toJSON(t, ref), value, "activity"); len(problems) > 0 {
		t.Fatalf("activity does not match its schema: %v", problems)
	}
	value["metadata"].(map[string]any)["addedLabelIds"] = "bug"
	if problems := validateSchema(schemas, toJSON(t, ref), value, "activity"); len(problems) == 0 {
		t.Fatal("schema accepted a scalar label delta")
	}
}
