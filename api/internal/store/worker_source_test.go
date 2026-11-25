package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW18WorkerTrustedSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected := func(data domain.Bootstrap) []string {
		result := []string{}
		for _, session := range data.AgentSessions {
			for _, message := range session.Messages {
				if message.Status == "streaming" && strings.HasPrefix(message.ID, "agent_message_run_") {
					result = append(result, session.ID+"\n"+session.UserID+"\n"+message.ID)
				}
			}
		}
		return result
	}
	for _, spec := range []struct{ name, sessionID, userID, messageID, status string }{
		{"status", idgen.New("agent_session_"), f.data.Viewer.ID, idgen.New("agent_message_run_"), "streaming\x00"},
		{"prefix", idgen.New("agent_session_"), f.data.Viewer.ID, "agent_mes\x00sage_run_probe", "streaming"},
		{"owner", idgen.New("agent_session_"), f.data.Viewer.ID + "\x00", idgen.New("agent_message_run_"), "streaming"},
		{"session", "agent_session_\x00probe", f.data.Viewer.ID, idgen.New("agent_message_run_"), "streaming"},
	} {
		t.Run(spec.name, func(t *testing.T) {
			stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "agentSessions"), workspace, "agent.session_updated", spec.sessionID, nil, func(data *domain.Bootstrap) error {
				data.AgentSessions = []domain.AgentSession{{
					ID: spec.sessionID, UserID: spec.userID, CreatedAt: stamp, UpdatedAt: stamp,
					Messages: []domain.AgentMessage{{
						ID: spec.messageID, Status: spec.status, Role: "assistant", UpdatedAt: &stamp, CreatedAt: stamp,
					}},
				}}
				return nil
			}); err != nil {
				t.Fatal("trusted typed mutation rejected", err)
			}
			warm, found := f.WorkspaceMetadataFields(workspace, "agentSessions")
			if !found || len(warm.AgentSessions) != 1 || warm.AgentSessions[0].UserID != spec.userID ||
				warm.AgentSessions[0].Messages[0].Status != spec.status || warm.AgentSessions[0].Messages[0].ID != spec.messageID {
				t.Fatal("trusted writer no longer retains exact installed fields")
			}
			canonical, found, err := scaleP21SW30CanonicalMetadataWitness(t.Context(), view, workspace, MetadataRecordScope{Fields: []string{"agentSessions"}})
			if err != nil || !found {
				t.Fatal("read canonical witness", found, err)
			}
			before, after := selected(warm), selected(canonical)
			if reflect.DeepEqual(before, after) {
				t.Fatal("installed/canonical source divergence unexpectedly disappeared")
			}
			if spec.name == "status" || spec.name == "prefix" {
				if len(before) != 0 || len(after) != 1 {
					t.Fatal("candidate selection witness changed", before, after)
				}
			} else if len(before) != 1 || len(after) != 1 || strings.ReplaceAll(before[0], "\x00", "") != after[0] {
				t.Fatal("candidate identity witness changed", before, after)
			}
			t.Logf("trusted %s producer: warm candidates=%d canonical candidates=%d", spec.name, len(before), len(after))
		})
	}
}
