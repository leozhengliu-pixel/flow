package domain

import (
	"testing"
	"time"
)

func TestAgentTaskTimedState(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *time.Time { value := now.Add(-ago); return &value }
	cases := []struct {
		name         string
		task         AgentTask
		status       string
		unresponsive bool
	}{
		{"acknowledged in time", AgentTask{Status: "pending", DeliveredAt: at(time.Minute), LastActivityAt: at(55 * time.Second)}, "pending", false},
		{"no acknowledgement within 10 s", AgentTask{Status: "pending", DeliveredAt: at(11 * time.Second)}, "pending", true},
		{"still within the window", AgentTask{Status: "pending", DeliveredAt: at(5 * time.Second)}, "pending", false},
		{"silent for 30 minutes", AgentTask{Status: "active", LastActivityAt: at(31 * time.Minute)}, "stale", false},
		{"never acknowledged and old", AgentTask{Status: "pending", DeliveredAt: at(40 * time.Minute)}, "stale", true},
		{"a new activity revives it", AgentTask{Status: "active", DeliveredAt: at(40 * time.Minute), LastActivityAt: at(time.Minute)}, "active", false},
		{"finished sessions keep their state", AgentTask{Status: "complete", LastActivityAt: at(time.Hour)}, "complete", false},
		{"built-in pending without delivery", AgentTask{Status: "pending"}, "pending", false},
	}
	for _, item := range cases {
		task := item.task
		task.DeriveTimedState(now)
		if task.Status != item.status || task.Unresponsive != item.unresponsive {
			t.Errorf("%s: status=%s unresponsive=%v, want %s %v", item.name, task.Status, task.Unresponsive, item.status, item.unresponsive)
		}
	}
	if staleAt := (AgentTask{Status: "active", LastActivityAt: at(0)}).StaleAt(); staleAt == nil || !staleAt.Equal(now.Add(AgentStaleAfter)) {
		t.Fatalf("StaleAt = %v", staleAt)
	}
}
