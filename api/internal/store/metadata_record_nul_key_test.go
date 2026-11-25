package store

import (
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// A record whose ID has NUL is stored without it on PostgreSQL. Replacing the
// collection must still delete that row (as on SQLite and MySQL), or it
// reappears after a reload.
func TestMetadataRecordNULKeyIsReplaced(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	seed := func(sessions ...domain.AgentSession) {
		t.Helper()
		for range 2 {
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "agentSessions"), workspace, "metadata_record_nul_key.seed", "", nil, func(data *domain.Bootstrap) error {
				data.AgentSessions = sessions
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(domain.AgentSession{ID: "nul-key-session\x00", UserID: "u", SlugID: "nul-key-slug"})
	seed(domain.AgentSession{ID: "nul-key-other", UserID: "u"}, domain.AgentSession{ID: "nul-key-session", UserID: "u"})
	seed(domain.AgentSession{ID: "nul-key-final", UserID: "u"})
	keys := func() []string {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions'`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := []string{}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				t.Fatal(err)
			}
			result = append(result, key)
		}
		return result
	}
	if got := keys(); !slices.Equal(got, []string{"nul-key-final"}) {
		t.Fatalf("stored session rows = %q, want only the final one", got)
	}
	if err := f.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := f.WorkspaceMetadataFields(workspace, "agentSessions")
	if len(reloaded.AgentSessions) != 1 || reloaded.AgentSessions[0].ID != "nul-key-final" {
		t.Fatalf("reloaded sessions = %+v", reloaded.AgentSessions)
	}
}
