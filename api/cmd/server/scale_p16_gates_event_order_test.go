package main

import (
	"reflect"
	"testing"
)

// Replay order is the durable sequence; RFC3339Nano text has variable precision.
const scaleP16DomainEventsSQL = "SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY seq"

func TestScaleP16GatesDomainEventOracleOrder(t *testing.T) {
	database := newScaleP04Database(t)
	for _, test := range []struct {
		name, first, second, third string
	}{
		{"fractional-prefix", "2026-11-14T01:38:30.75Z", "2026-11-14T01:38:30.757Z", "2026-11-14T01:38:30.758Z"},
		{"same-wall-time", "2026-11-14T01:38:30.75Z", "2026-11-14T01:38:30.75Z", "2026-11-14T01:38:30.75Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Shadow only this SELECT's table, preserving the differential's
			// exact projection and ordering on every configured SQL driver.
			query := `WITH domain_events AS (
				SELECT 1 AS seq,'z-first' AS id,'` + test.first + `' AS created_at,
					'project.update_commented' AS event_type,'project' AS aggregate_id,
					'{"body":"Update comment"}' AS payload,'' AS previous_values,'p16-order' AS workspace_id
				UNION ALL SELECT 2,'m-second','` + test.second + `',
					'project.update_reaction_toggled','project','{"emoji":"+1"}','','p16-order'
				UNION ALL SELECT 3,'a-third','` + test.third + `',
					'project.update_deleted','project','{"id":"update"}','','p16-order'
			) ` + scaleP16DomainEventsSQL
			rows := database.query(t, query, "p16-order")
			defer rows.Close()
			var got [][]string
			for rows.Next() {
				record := make([]string, 4)
				if err := rows.Scan(&record[0], &record[1], &record[2], &record[3]); err != nil {
					t.Fatal(err)
				}
				got = append(got, record)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := [][]string{
				{"project.update_commented", "project", `{"body":"Update comment"}`, ""},
				{"project.update_reaction_toggled", "project", `{"emoji":"+1"}`, ""},
				{"project.update_deleted", "project", `{"id":"update"}`, ""},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("event oracle changed causal sequence or fields: got=%v want=%v", got, want)
			}
		})
	}
}
