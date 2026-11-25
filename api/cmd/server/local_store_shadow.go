package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"

	"flow/api/internal/store"
)

const localStoreHeader = "X-Flow-Local-Store"
const localStoreDisabledValue = "disabled"
const localStoreShadowCapability = "local-store-shadow-v1"
const localStoreIssueCapability = "local-store-issues-v1"

type localStoreStamp struct {
	ViewerID    string `json:"viewerId"`
	WorkspaceID string `json:"workspaceId"`
	AuthzEpoch  string `json:"authzEpoch"`
	Head        string `json:"head"`
}

func localStoreDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_LOCAL_STORE"))) {
	case "off", "0", "false":
		return true
	default:
		return false
	}
}

func (s *server) localStoreAllowed(workspace string) bool {
	return workspace != "" && !localStoreDisabled() && s.store.SyncV2Enabled() &&
		syncFlagEnabled("FLOW_SYNC_MANIFEST") && syncFlagEnabled("FLOW_INDEXED_READS")
}

func localStoreBrowserViewer(r *http.Request) string {
	if r.Context().Value(apiKeyContextKey{}) != nil || r.Header.Get("Authorization") != "" {
		return ""
	}
	return authUser(r).ID
}

func (s *server) localStoreAuthority(r *http.Request, workspace string) (localStoreStamp, bool) {
	viewer := localStoreBrowserViewer(r)
	if viewer == "" || !s.localStoreAllowed(workspace) {
		return localStoreStamp{}, false
	}
	if _, ready := s.syncV2Journal(); !ready {
		return localStoreStamp{}, false
	}
	ctx := r.Context()
	access, err := s.store.ResolveAccess(ctx, workspace, viewer, nil)
	if err != nil || access.AuthzEpoch == "" {
		return localStoreStamp{}, false
	}
	head, err := s.store.RecordJournalHead(ctx, access.WorkspaceID)
	if err != nil || s.store.ValidateAccess(ctx, access) != nil {
		return localStoreStamp{}, false
	}
	return localStoreStamp{ViewerID: viewer, WorkspaceID: access.WorkspaceID, AuthzEpoch: access.AuthzEpoch, Head: head}, true
}

func (s *server) stampLocalStoreResponse(w http.ResponseWriter, r *http.Request, workspace string) {
	requested := r.Header.Get(localStoreHeader)
	if (requested != "" || r.URL.Path == "/api/auth/session" ||
		strings.HasPrefix(r.URL.Path, "/api/sync/") && r.URL.Query().Get("surface") == "local-store") && localStoreDisabled() {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set(localStoreHeader, localStoreDisabledValue)
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/api/bootstrap" && r.URL.Path != "/api/issue-records/bootstrap" ||
		requested != localStoreShadowCapability || r.Header.Get("X-Flow-Projection") != "" {
		return
	}
	stamp, ok := s.localStoreAuthority(r, workspace)
	if !ok {
		return
	}
	raw, err := json.Marshal(stamp)
	if err == nil {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set(localStoreHeader, string(raw))
	}
}

// The shadow manifest reports authority and the journal cursor. The
// general manifest still advertises only the collections it can page.
func (s *server) localStoreManifest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("mode") == "issues" {
		s.localStoreIssueManifest(w, r)
		return
	}
	stamp, ok := s.localStoreAuthority(r, workspaceKey(r))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeSyncJSON(w, struct {
		localStoreStamp
		Schema       int      `json:"schema"`
		Capabilities []string `json:"capabilities"`
	}{stamp, 2, []string{localStoreShadowCapability}}, 4096)
}

func (s *server) localStoreChanges(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("surface") != "local-store" {
		return false
	}
	if viewer := r.Header.Get(localStoreHeader); viewer == "" || viewer != localStoreBrowserViewer(r) {
		writeError(w, http.StatusForbidden, "Sync viewer changed")
		return true
	}
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "Missing sync workspace")
		return true
	}
	journal, ready := s.syncV2Journal()
	if !ready || !s.localStoreAllowed(workspaceKey(r)) {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL.Query().Get("mode") == "issues" {
		s.localStoreIssueChanges(w, r, journal, workspaceID)
	} else {
		s.syncChangesWithJournal(w, r, localStoreShadowJournal{committedChangeReader: journal, workspaceID: workspaceID})
	}
	return true
}

type localStoreShadowJournal struct {
	committedChangeReader
	workspaceID string
}

func (reader localStoreShadowJournal) ReadCommittedChanges(ctx context.Context, workspace, after string, limit, budget int) (store.CommittedPage, error) {
	if workspace != reader.workspaceID {
		return store.CommittedPage{Head: "0", Next: after, ResyncRequired: true}, nil
	}
	page, err := reader.committedChangeReader.ReadCommittedChanges(ctx, workspace, after, limit, budget)
	if err != nil {
		return page, err
	}
	// The shadow does not store these collections. Keep scope and unknown invalidations.
	page.Transactions = slices.Clone(page.Transactions)
	for i := range page.Transactions {
		tx := &page.Transactions[i]
		tx.Records = slices.DeleteFunc(slices.Clone(tx.Records), func(record store.RecordDelta) bool { return localStoreIgnoresKind(record.Key.Kind) })
		tx.Invalidations = slices.DeleteFunc(slices.Clone(tx.Invalidations), localStoreIgnoresKind)
	}
	return page, nil
}

func localStoreIgnoresKind(kind string) bool {
	switch kind {
	case "issue_field_history", "activities", "comments", "userSettings", "settings":
		return true
	default:
		return false
	}
}
