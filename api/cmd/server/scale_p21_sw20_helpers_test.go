package main

import (
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW20Rows(t *testing.T, repo *store.SQLiteStore, workspace string, keys ...store.RecordKey) []any {
	t.Helper()
	batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, keys, "detail")
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{}
	for _, item := range batch.Items {
		if item.Omitted {
			t.Fatal("small owned row omitted", item.Key)
		}
		rows = append(rows, []any{item.Key, item.Missing, item.Data})
	}
	return rows
}

func scaleP21SW20Effects(events []domain.DomainEvent, realtime []domain.RealtimeEvent) []any {
	effects := []any{}
	for _, event := range events {
		effects = append(effects, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
	}
	for _, event := range realtime {
		effects = append(effects, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
	}
	return effects
}
