package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SW291: a loop's uploads follow the loop's read rule. Every active member
// (admins, members outside the loop's private team, guests, apps) reads every
// loop through GET /api/loops, so a file on an existing loop is served to all
// of them, disabled or draft alike; once the loop is deleted, neither the copy
// on the loop nor the claimed upload record serves its file, not even to the
// uploader. A pending upload stays its uploader's. Legacy and bounded checks
// answer identically, warm and SQL-reloaded.
func TestScaleP21SW291LoopAttachmentVisibility(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW268Seed(t, repo)
		workspace := f.data.Workspace.URLKey
		prefix := idgen.New("sw291-")
		stamp := time.Date(2026, time.November, 17, 10, 0, 0, 0, time.UTC)
		url := func(name string) string { return "/uploads/" + prefix + name + ".txt" }
		byName := map[string]domain.User{}
		for _, principal := range f.principals {
			byName[principal.name] = principal.user
		}
		owner, member, guest := byName["admin-private-outsider"], byName["member-private-outsider"], byName["guest-explicit"]
		private := ""
		loopAttachment := func(name, loopID string, creator domain.User) domain.LoopAttachment {
			return domain.LoopAttachment{ID: prefix + name, LoopID: loopID, Name: name + ".txt", ContentType: "text/plain", Size: 4, URL: url(name), CreatorID: creator.ID, CreatedAt: stamp}
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw291_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if team.Private && private == "" {
					private = team.ID
				}
			}
			if private == "" {
				return fmt.Errorf("fixture lacks its private team")
			}
			claimed := []domain.LoopAttachment{
				loopAttachment("private-loop", prefix+"private", member),
				loopAttachment("disabled-loop", prefix+"disabled", member),
				loopAttachment("deleted-loop", prefix+"deleted", member),
				loopAttachment("deleted-claim-only", prefix+"deleted", member),
				loopAttachment("orphan-claim", prefix+"never-existed", member),
			}
			loop := func(id, name, level, team, status string, enabled bool, attachments ...domain.LoopAttachment) domain.Loop {
				return domain.Loop{
					ID: id, Name: name, Level: level, TeamID: team, Status: status, Enabled: enabled, TriggerType: "schedule",
					Instructions: "Summarize the week.", OwnerID: owner.ID, Creator: owner, CreatedAt: stamp, UpdatedAt: stamp,
					Attachments: attachments,
				}
			}
			data.Loops = append(data.Loops,
				loop(prefix+"private", "SW291 private team loop", "team", private, "published", true, claimed[0]),
				loop(prefix+"disabled", "SW291 disabled loop", "workspace", "", "published", false, claimed[1]),
				loop(prefix+"deleted", "SW291 deleted loop", "workspace", "", "published", false, claimed[2]),
			)
			data.LoopAttachments = append(data.LoopAttachments, claimed...)
			data.LoopAttachments = append(data.LoopAttachments, loopAttachment("guest-pending", "", guest))
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"private-loop", "disabled-loop", "deleted-loop", "deleted-claim-only", "orphan-claim", "guest-pending"}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(f.uploads, strings.TrimPrefix(url(name), "/uploads/")), []byte("file"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// The loop list every principal reads names the private team's loop
		// and its file: the rule the upload check follows.
		service := &server{store: repo, uploadPath: f.uploads}
		for _, principal := range f.principals {
			writer := httptest.NewRecorder()
			r := f.literalRequest(principal.user, nil, nil)
			r.Method, r.URL.Path = http.MethodGet, "/api/loops"
			service.listLoops(writer, r)
			var loops []domain.Loop
			if writer.Code != http.StatusOK || json.Unmarshal(writer.Body.Bytes(), &loops) != nil {
				t.Fatalf("%s: list loops %d %s", principal.name, writer.Code, writer.Body.String())
			}
			index := slices.IndexFunc(loops, func(loop domain.Loop) bool { return loop.ID == prefix+"private" })
			if index < 0 || len(loops[index].Attachments) != 1 || loops[index].Attachments[0].URL != url("private-loop") {
				t.Fatalf("%s does not read the private team's loop and its file", principal.name)
			}
		}
		// Delete through the app's own handler: the loop goes, its claimed
		// upload records stay behind.
		writer := httptest.NewRecorder()
		r := f.literalRequest(owner, nil, nil)
		r.Method, r.URL.Path = http.MethodDelete, "/api/loops/"+prefix+"deleted"
		r.SetPathValue("id", prefix+"deleted")
		service.deleteLoop(writer, r)
		if writer.Code != http.StatusNoContent {
			t.Fatalf("delete loop %d %s", writer.Code, writer.Body.String())
		}
		want := map[string]map[string]bool{}
		for _, principal := range f.principals {
			want[principal.name] = map[string]bool{
				"private-loop": true, "disabled-loop": true,
				"deleted-loop": false, "deleted-claim-only": false, "orphan-claim": false,
				"guest-pending": principal.user.ID == guest.ID,
			}
		}
		for _, reload := range []bool{false, true} {
			if reload {
				for _, key := range []string{workspace, f.other} {
					if err := repo.ReloadWorkspace(t.Context(), key); err != nil {
						t.Fatal(err)
					}
				}
			}
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			for _, principal := range f.principals {
				for _, name := range names {
					t.Run(fmt.Sprintf("reload=%t/%s/%s", reload, principal.name, name), func(t *testing.T) {
						legacy := scaleP21SW268Serve(t, f, repo, principal.user, url(name))
						bounded := scaleP21SW268Serve(t, f, view, principal.user, url(name))
						if !reflect.DeepEqual(legacy, bounded) {
							t.Fatalf("upload visibility differs:\nlegacy=%+v\nbounded=%+v", legacy, bounded)
						}
						status := http.StatusNotFound
						if want[principal.name][name] {
							status = http.StatusOK
						}
						if legacy.Status != status || legacy.Readable != (status == http.StatusOK) {
							t.Fatalf("status=%d readable=%t, want %d", legacy.Status, legacy.Readable, status)
						}
						if status == http.StatusOK && legacy.Body != "file" {
							t.Fatalf("served %q", legacy.Body)
						}
					})
				}
			}
		}
	}
}
