package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"flow/api/internal/store"
)

func TestScaleP21SW50AppendSQLAttribution(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		op := scaleP21SW50AppendOperations()[0]
		scaleP21SW50ResetAppend(t, f, op, true)
		request := f.request(f.data.Viewer, nil, op.body)
		counts := map[string]int{}
		request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
			table := "other"
			for _, name := range []string{"workspace_states", "workspace_metadata_records"} {
				if strings.Contains(query, name) {
					table = name
					break
				}
			}
			var pcs [48]uintptr
			size := runtime.Callers(2, pcs[:])
			frames := runtime.CallersFrames(pcs[:size])
			seen := map[string]bool{}
			for {
				frame, more := frames.Next()
				for _, name := range []string{"readResidentWorkspace", "decodeWorkspaceMetadata", "prepareMetadataAppends", "scaleP16WriteChanges", "persistWorkspaceTx", "readMetadataCommitStamps", "readMetadataStamp", "WorkspaceIDForKey", "WorkspaceRole", "actorTeamIDs"} {
					if strings.Contains(frame.Function, "."+name) {
						seen[name] = true
					}
				}
				if !more {
					break
				}
			}
			phase := "direct-or-other"
			for _, name := range []string{"readResidentWorkspace", "readMetadataCommitStamps", "prepareMetadataAppends", "persistWorkspaceTx", "scaleP16WriteChanges", "decodeWorkspaceMetadata", "readMetadataStamp", "WorkspaceIDForKey", "WorkspaceRole", "actorTeamIDs"} {
				if seen[name] {
					phase = name
					break
				}
			}
			counts[phase+"/"+table]++
		}))
		response := httptest.NewRecorder()
		op.run(&server{store: f.probe}, response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("bounded creation failed: %d %s", response.Code, response.Body.String())
		}
		t.Logf("bounded append primary SQL phases=%v", counts)
	}
}
