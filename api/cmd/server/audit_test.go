package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21M3AuditDifferential(t *testing.T) {
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "coding-mcp-audit", kind: "settings", id: "m3-audit-sentinel", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.Settings["m3-audit-sentinel"] = "unchanged"
			},
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				var caller codingSandboxCaller
				caller.grant.WorkspaceKey = workspaceKey(r)
				caller.grant.SandboxID = "m3-sandbox"
				caller.session.ID, caller.session.CreatorID = "m3-coding", authUser(r).ID
				s.auditCodingMCPToolCall(caller, applicationPolicy{ID: "m3-policy", Name: "Connector"}, "read", json.RawMessage(`{"content":[]}`), nil, time.Second)
				w.WriteHeader(http.StatusNoContent)
			},
		},
	})
}
