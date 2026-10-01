package main

import (
	"testing"

	"flow/api/internal/domain"
)

// Post-write loop checks skip the workspace snapshot for events no loop can
// match; the filter must accept every event resolveLoopEvent handles.
func TestLoopEventCandidate(t *testing.T) {
	for eventType, want := range map[string]bool{
		"issue.created": true, "issue.updated": true, "comment.created": true,
		"customer_request.created": true, "customer_request.updated": true,
		"project.updated": true, "project.update_created": true, "initiative.update_created": true,
		"release.created": true, "team.updated": true, "cycle.started": true, "cycle.completed": true,
		"issue.deleted": false, "pulse.summary_scheduled": false, "team_member.cleaned_up": false,
		"loop.run_progress": false, "notification_preferences.updated": false, "cycle.deleted": false,
	} {
		if got := loopEventCandidate(domain.DomainEvent{Type: eventType, AggregateID: "id"}); got != want {
			t.Errorf("loopEventCandidate(%s) = %v, want %v", eventType, got, want)
		}
	}
	if loopEventCandidate(domain.DomainEvent{Type: "issue.updated"}) {
		t.Error("an event without an aggregate cannot trigger a loop")
	}
}
