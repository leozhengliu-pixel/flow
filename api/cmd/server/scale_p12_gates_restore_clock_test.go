package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func scaleP12GatesNormalizeRestoreClocks(value []any, targetID string) ([]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var outcome []any
	if err := json.Unmarshal(raw, &outcome); err != nil {
		return nil, err
	}
	if len(outcome) != 5 {
		return nil, fmt.Errorf("restore outcome has %d fields", len(outcome))
	}
	response, ok := outcome[1].(map[string]any)
	if !ok || response["id"] != targetID {
		return nil, fmt.Errorf("restore response does not identify its target")
	}
	clock, ok := response["updatedAt"].(string)
	if _, err := time.Parse(time.RFC3339Nano, clock); !ok || err != nil {
		return nil, fmt.Errorf("restore response has an invalid clock")
	}
	rows, ok := outcome[2].([]any)
	if !ok {
		return nil, fmt.Errorf("restore canonical rows are missing")
	}
	var target map[string]any
	for _, item := range rows {
		row, ok := item.([]any)
		if !ok || len(row) != 3 {
			return nil, fmt.Errorf("restore canonical row has an invalid shape")
		}
		key, ok := row[0].(map[string]any)
		if !ok || key["Kind"] != "teams" || key["ID"] != targetID {
			continue
		}
		target, ok = row[2].(map[string]any)
		if !ok || row[1] != false || target["id"] != targetID || target["updatedAt"] != clock {
			return nil, fmt.Errorf("restore response and canonical target clocks differ")
		}
	}
	if target == nil {
		return nil, fmt.Errorf("restore canonical target is missing")
	}
	normalizeTeam := func(value any) {
		team, ok := value.(map[string]any)
		if ok && team["id"] == targetID && team["updatedAt"] == clock {
			team["updatedAt"] = "<restored-clock>"
		}
	}
	normalizeTeam(response)
	normalizeTeam(target)
	audits, ok := outcome[3].([]any)
	if !ok {
		return nil, fmt.Errorf("restore audit entries are missing")
	}
	for _, item := range audits {
		audit, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("restore audit entry has an invalid shape")
		}
		createdAt, ok := audit["createdAt"].(string)
		if _, err := time.Parse(time.RFC3339Nano, createdAt); !ok || err != nil {
			return nil, fmt.Errorf("restore audit entry has an invalid clock")
		}
		audit["createdAt"] = "<audit-clock>"
	}
	effects, ok := outcome[4].([]any)
	if !ok {
		return nil, fmt.Errorf("restore effects are missing")
	}
	for _, item := range effects {
		effect, ok := item.([]any)
		if !ok || len(effect) != 5 && len(effect) != 6 {
			return nil, fmt.Errorf("restore effect has an invalid shape")
		}
		if effect[0] != "team.unarchived" || effect[1] != targetID {
			continue
		}
		payload := effect[4]
		if len(effect) == 6 {
			payload = effect[5]
		}
		if envelope, ok := payload.(map[string]any); ok {
			normalizeTeam(envelope["entity"])
		}
	}
	return outcome, nil
}

func TestScaleP12GatesRestoreClockNormalization(t *testing.T) {
	const targetID = "team_restore_clock"
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := func(offset time.Duration) string { return stamp.Add(offset).Format(time.RFC3339Nano) }
	makeOutcome := func(restored, audited string) []any {
		team := func() map[string]any {
			return map[string]any{"id": targetID, "name": "Restorable", "private": true, "updatedAt": restored}
		}
		return []any{
			http.StatusOK, team(),
			[]any{[]any{map[string]any{"Kind": "teams", "ID": targetID}, false, team()}},
			[]any{map[string]any{"id": "audit-fixed", "createdAt": audited, "action": "restored", "metadata": map[string]any{"name": "Restorable"}}},
			[]any{
				[]any{"team.unarchived", targetID, "actor-fixed", map[string]any{"body": "retained"}, nil, map[string]any{"entity": team()}},
				[]any{"team.unarchived", targetID, "actor-fixed", "client-fixed", map[string]any{"entity": team()}},
			},
		}
	}
	normalize := func(t *testing.T, value []any) []any {
		t.Helper()
		normalized, err := scaleP12GatesNormalizeRestoreClocks(value, targetID)
		if err != nil {
			t.Fatal(err)
		}
		return normalized
	}
	t.Run("old-value-map-collision", func(t *testing.T) {
		left := scaleP21SW6GeneratedJSON(t, makeOutcome(clock(0), clock(time.Nanosecond)), map[string]string{
			clock(0): "<restored-clock>", clock(time.Nanosecond): "<audit-clock>",
		})
		generated := map[string]string{clock(time.Second): "<restored-clock>"}
		generated[clock(time.Second)] = "<audit-clock>"
		right := scaleP21SW6GeneratedJSON(t, makeOutcome(clock(time.Second), clock(time.Second)), generated)
		if reflect.DeepEqual(left, right) ||
			left.([]any)[1].(map[string]any)["updatedAt"] != "<restored-clock>" ||
			right.([]any)[1].(map[string]any)["updatedAt"] != "<audit-clock>" {
			t.Fatal("old normalizer did not expose the deterministic clock-key collision")
		}
	})
	for _, spec := range []struct {
		name                  string
		leftAudit, rightAudit time.Duration
	}{
		{"distinct-clocks", time.Nanosecond, time.Second + time.Nanosecond},
		{"right-collision", time.Nanosecond, time.Second},
		{"left-collision", 0, time.Second + time.Nanosecond},
		{"both-collision", 0, time.Second},
	} {
		t.Run(spec.name, func(t *testing.T) {
			left := normalize(t, makeOutcome(clock(0), clock(spec.leftAudit)))
			right := normalize(t, makeOutcome(clock(time.Second), clock(spec.rightAudit)))
			if !reflect.DeepEqual(left, right) {
				t.Fatal("semantic clock normalization changed the restore comparison")
			}
		})
	}
	t.Run("canonical-clock-must-match-response", func(t *testing.T) {
		value := makeOutcome(clock(0), clock(time.Nanosecond))
		value[2].([]any)[0].([]any)[2].(map[string]any)["updatedAt"] = clock(time.Second)
		if _, err := scaleP12GatesNormalizeRestoreClocks(value, targetID); err == nil {
			t.Fatal("canonical target clock mismatch was normalized away")
		}
	})
	t.Run("literal-clocks-are-not-normalized", func(t *testing.T) {
		var normalized []any
		for _, restored := range []string{clock(0), clock(time.Second)} {
			value := makeOutcome(restored, restored)
			value[1].(map[string]any)["externalSource"] = restored
			value[2].([]any)[0].([]any)[2].(map[string]any)["externalSource"] = restored
			value[3].([]any)[0].(map[string]any)["metadata"].(map[string]any)["name"] = restored
			effect := value[4].([]any)[0].([]any)
			effect[2] = restored
			effect[3].(map[string]any)["body"] = restored
			effect[5].(map[string]any)["actor"] = map[string]any{"id": targetID, "updatedAt": restored}
			result := normalize(t, value)
			effect = result[4].([]any)[0].([]any)
			if result[1].(map[string]any)["externalSource"] != restored ||
				result[2].([]any)[0].([]any)[2].(map[string]any)["externalSource"] != restored ||
				result[3].([]any)[0].(map[string]any)["metadata"].(map[string]any)["name"] != restored ||
				effect[2] != restored || effect[3].(map[string]any)["body"] != restored ||
				effect[5].(map[string]any)["actor"].(map[string]any)["updatedAt"] != restored {
				t.Fatal("business value matching a clock was rewritten")
			}
			normalized = append(normalized, result)
		}
		if reflect.DeepEqual(normalized[0], normalized[1]) {
			t.Fatal("different literal clock strings were normalized away")
		}
	})
	for _, spec := range []struct {
		name   string
		change func([]any)
	}{
		{"status", func(v []any) { v[0] = http.StatusForbidden }},
		{"response-name", func(v []any) { v[1].(map[string]any)["name"] = "Changed" }},
		{"target-private", func(v []any) { v[2].([]any)[0].([]any)[2].(map[string]any)["private"] = false }},
		{"audit-action", func(v []any) { v[3].([]any)[0].(map[string]any)["action"] = "purged" }},
		{"audit-metadata", func(v []any) { v[3].([]any)[0].(map[string]any)["metadata"].(map[string]any)["name"] = "Changed" }},
		{"domain-entity", func(v []any) {
			v[4].([]any)[0].([]any)[5].(map[string]any)["entity"].(map[string]any)["name"] = "Changed"
		}},
		{"realtime-clock", func(v []any) {
			v[4].([]any)[1].([]any)[4].(map[string]any)["entity"].(map[string]any)["updatedAt"] = clock(2 * time.Second)
		}},
		{"body-matches-clock", func(v []any) { v[4].([]any)[0].([]any)[3].(map[string]any)["body"] = clock(time.Second) }},
		{"actor-matches-clock", func(v []any) { v[4].([]any)[0].([]any)[2] = clock(time.Second) }},
		{"audit-metadata-matches-clock", func(v []any) {
			v[3].([]any)[0].(map[string]any)["metadata"].(map[string]any)["name"] = clock(time.Second)
		}},
	} {
		t.Run(spec.name, func(t *testing.T) {
			left := normalize(t, makeOutcome(clock(0), clock(time.Nanosecond)))
			right := makeOutcome(clock(time.Second), clock(time.Second))
			spec.change(right)
			if reflect.DeepEqual(left, normalize(t, right)) {
				t.Fatal("business difference was normalized away")
			}
		})
	}
}
