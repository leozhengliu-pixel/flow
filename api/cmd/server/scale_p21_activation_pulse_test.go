package main

import (
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21ActivationPulseDifferential(t *testing.T) {
	for _, mode := range []string{"off", "current", "full"} {
		t.Run(mode, func(t *testing.T) {
			peers, fixture, _, project := scaleP21ActivationHTTPPeers(t, mode)
			workspace := fixture.data.Workspace
			for writer := range peers {
				var cursor string
				if mode != "off" {
					var err error
					cursor, err = peers[writer].service.store.RecordJournalHead(t.Context(), workspace.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				legacyHead, err := peers[writer].service.store.WorkspaceChangeHead(t.Context(), workspace.URLKey)
				if err != nil {
					t.Fatal(err)
				}
				created := authRequest[domain.ProjectUpdate](t, fixture.admin, http.MethodPost, peers[writer].URL+"/api/projects/"+project+"/updates",
					map[string]string{"body": "Activation update", "health": "onTrack"}, workspace.URLKey, http.StatusCreated)
				var peerItems [2]pulseFeedItem
				var transactions [2]syncV2Transaction
				for reader, peer := range peers {
					feed := authRequest[pulseFeedResponse](t, fixture.admin, http.MethodGet, peer.URL+"/api/pulse/feed?view=all", nil, workspace.URLKey, http.StatusOK)
					for _, item := range feed.Items {
						if update, ok := item.Update.(map[string]any); ok && update["id"] == created.ID && update["body"] == created.Body {
							peerItems[reader] = item
							break
						}
					}
					legacy := authRequest[struct{ Events []domain.RealtimeEvent }](t, fixture.admin, http.MethodGet,
						peer.URL+"/api/realtime/changes?since="+strconv.FormatInt(legacyHead, 10), nil, workspace.URLKey, http.StatusOK)
					index := slices.IndexFunc(legacy.Events, func(event domain.RealtimeEvent) bool {
						return event.Type == "project.update_created" && event.AggregateID == project
					})
					if index < 0 {
						t.Fatal("legacy replay did not include the project update")
					}
					if mode != "off" {
						seq, err := peer.service.store.RecordJournalEventSeq(t.Context(), legacy.Events[index].ID)
						if err != nil {
							t.Fatal("project update did not have a journal sequence")
						}
						page := authRequest[syncV2ChangesResponse](t, fixture.admin, http.MethodGet,
							peer.URL+"/api/sync/changes?after="+cursor, nil, workspace.URLKey, http.StatusOK)
						index := slices.IndexFunc(page.Transactions, func(tx syncV2Transaction) bool { return tx.Seq == seq })
						if page.ResyncRequired || index < 0 || !slices.Contains(page.Transactions[index].Invalidations, "projectUpdates") {
							t.Fatal("sync replay did not invalidate project updates")
						}
						transactions[reader] = page.Transactions[index]
					}
				}
				if peerItems[0].ID == "" || !reflect.DeepEqual(peerItems[0], peerItems[1]) {
					t.Fatal("Pulse feeds did not include the same new project update")
				}
				if !reflect.DeepEqual(transactions[0], transactions[1]) {
					t.Fatal("replicas disagreed on the committed Pulse transaction")
				}
			}
		})
	}
}
