package main

import (
	"net/http"
	"testing"

	"flow/api/internal/domain"
)

// A run keeps the version it ran after the loop is published again, and that
// version's instructions stay readable: the run page shows them instead of
// the current ones.
func TestLoopRunKeepsTheInstructionsOfItsVersion(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	provider.script(`tool:finish_run {"status":"done","summary":"已追加。"}`)
	loop, started := startTestRun(t, handler, "团队日报", "将总结追加到已有团队文档", nil)
	ran := waitForRun(t, handler, loop.ID, started.ID)
	edited := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructions": "只回复总结文本，不要写入任何文档或其他资源"}, http.StatusOK)
	if ran.Version != 1 || ran.VersionID != loop.ID+"_v1" || edited.Version != 2 {
		t.Fatalf("run version %d %q, loop version after the edit %d", ran.Version, ran.VersionID, edited.Version)
	}
	run := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+started.ID, nil, http.StatusOK)
	version := requestJSON[domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/versions/"+run.VersionID, nil, http.StatusOK)
	if run.Version != 1 || version.Version != 1 || version.Current || version.Definition.Instructions != "将总结追加到已有团队文档" {
		t.Fatalf("run %d %q; its version = %d current=%v %q", run.Version, run.VersionID, version.Version, version.Current, version.Definition.Instructions)
	}
}
