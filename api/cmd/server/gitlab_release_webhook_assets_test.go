package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// gitlabAssetsPayload is the documented Release Hook shape: assets.count,
// assets.links[] (id, external, link_type, name, url) and assets.sources[].
func gitlabAssetsPayload(action, tag string, links ...map[string]any) map[string]any {
	payload := gitlabReleasePayload(action, tag, "Release "+tag, "Notes")
	payload["created_at"] = "2026-10-20 08:00:00 UTC"
	payload["released_at"] = "2026-10-20 08:00:00 UTC"
	payload["id"] = 1
	payload["assets"] = map[string]any{
		"count": len(links) + 2,
		"links": links,
		"sources": []map[string]any{
			{"format": "zip", "url": "https://gitlab.example.com/acme/app/-/archive/" + tag + "/app-" + tag + ".zip"},
			{"format": "tar.gz", "url": "https://gitlab.example.com/acme/app/-/archive/" + tag + "/app-" + tag + ".tar.gz"},
		},
	}
	return payload
}

func gitlabAssetLink(name, address, linkType string) map[string]any {
	return map[string]any{"id": 1, "external": true, "link_type": linkType, "name": name, "url": address}
}

func (e *gitlabReleaseHookEnv) linkResources(id string) map[string]domain.ReleaseResource {
	e.t.Helper()
	stored := requestJSON[domain.Release](e.t, e.handler, http.MethodGet, "/api/releases/"+id, nil, http.StatusOK)
	links := map[string]domain.ReleaseResource{}
	for _, item := range stored.Resources {
		if item.Type != "link" {
			continue
		}
		if _, dup := links[item.URL]; dup {
			e.t.Fatalf("duplicate link %s: %#v", item.URL, stored.Resources)
		}
		links[item.URL] = item
	}
	return links
}

func TestGitLabReleaseHookAssetLinksReconcile(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	pipeline := e.pipeline(scheduledReleasePipeline)
	page := "https://gitlab.example.com/acme/app/-/releases/v2.0.0"
	installer := "https://example.invalid/qa/installer.zip"
	image := "https://example.invalid/qa/image.tar"

	created := e.release("", gitlabAssetsPayload("create", "v2.0.0", gitlabAssetLink("QA installer", installer, "package"), gitlabAssetLink("Image", image, "image")), http.StatusAccepted)
	if !created.Created || created.Release == nil {
		t.Fatalf("create = %#v", created)
	}
	id := e.releases(pipeline.ID)[0].ID
	links := e.linkResources(id)
	if len(links) != 3 || links[page].Title != "GitLab release" || links[installer].Title != "QA installer" || links[image].Title != "Image" {
		t.Fatalf("created links = %#v", links)
	}
	for address, link := range links {
		if link.Source != "gitlab" || strings.Contains(address, "/archive/") {
			t.Fatalf("link %s = %#v (source archives must be skipped)", address, link)
		}
	}

	// A user adds links in Flow (one of them is a URL GitLab will later list too).
	stored := requestJSON[domain.Release](t, e.handler, http.MethodGet, "/api/releases/"+id, nil, http.StatusOK)
	manual := "https://wiki.example.com/rollout"
	sums := "https://example.invalid/qa/sums.txt"
	stored.Resources = append(stored.Resources,
		domain.ReleaseResource{ID: "manual_1", Type: "link", Title: "Rollout plan", URL: manual, CreatedAt: stored.CreatedAt},
		domain.ReleaseResource{ID: "manual_2", Type: "link", Title: "My checksum", URL: sums, CreatedAt: stored.CreatedAt})
	requestJSON[domain.Release](t, e.handler, http.MethodPatch, "/api/releases/"+id, map[string]any{"resources": stored.Resources}, http.StatusOK)
	if links = e.linkResources(id); links[installer].Source != "gitlab" {
		t.Fatalf("saving resources dropped the owner marker: %#v", links[installer])
	}

	// Redelivery is idempotent.
	e.release("", gitlabAssetsPayload("create", "v2.0.0", gitlabAssetLink("QA installer", installer, "package"), gitlabAssetLink("Image", image, "image")), http.StatusAccepted)
	if links = e.linkResources(id); len(links) != 5 {
		t.Fatalf("redelivery changed links: %#v", links)
	}
	// Update: rename one, remove one, add one that is already a manual link,
	// and two unsafe URLs.
	result := e.release("", gitlabAssetsPayload("update", "v2.0.0",
		gitlabAssetLink("QA installer (signed)", installer, "package"),
		gitlabAssetLink("Checksums", sums, "other"),
		gitlabAssetLink("Evil", "javascript:alert(1)", "other"),
		gitlabAssetLink("Data", "data:text/html;base64,AAAA", "other")), http.StatusAccepted)
	if result.Created || result.RejectedLinks != 2 {
		t.Fatalf("update = %#v", result)
	}
	links = e.linkResources(id)
	if len(links) != 4 || links[page].Source != "gitlab" || links[installer].Title != "QA installer (signed)" || links[installer].Source != "gitlab" {
		t.Fatalf("updated links = %#v", links)
	}
	if _, kept := links[image]; kept {
		t.Fatalf("removed GitLab link survived: %#v", links)
	}
	if link := links[manual]; link.Title != "Rollout plan" || link.Source != "" {
		t.Fatalf("manual link changed: %#v", link)
	}
	if link := links[sums]; link.Title != "My checksum" || link.Source != "" {
		t.Fatalf("manual link claimed by GitLab: %#v", link)
	}
	for address := range links {
		if strings.HasPrefix(address, "javascript:") || strings.HasPrefix(address, "data:") {
			t.Fatalf("unsafe link stored: %s", address)
		}
	}
	if len(e.releases(pipeline.ID)) != 1 {
		t.Fatal("release duplicated")
	}
}

func TestGitLabReleaseHookLinkCaps(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	pipeline := e.pipeline(scheduledReleasePipeline)
	var many []map[string]any
	for i := 0; i < 80; i++ {
		many = append(many, gitlabAssetLink(fmt.Sprintf("Asset %d", i), fmt.Sprintf("https://example.invalid/a/%d.zip", i), "other"))
	}
	many = append(many, gitlabAssetLink(strings.Repeat("n", 500), "https://example.invalid/long", "other"), gitlabAssetLink("Long", "https://example.invalid/"+strings.Repeat("x", 3000), "other"))
	result := e.release("", gitlabAssetsPayload("create", "v3.0.0", many...), http.StatusAccepted)
	links := e.linkResources(e.releases(pipeline.ID)[0].ID)
	// The release page plus 49 assets fit; the other 31 valid assets and the
	// capped-out long-name link hit the cap, the oversized URL is invalid.
	if len(links) != gitlabReleaseMaxLinks || result.RejectedLinks != 31+1+1 {
		t.Fatalf("links = %d rejected = %d", len(links), result.RejectedLinks)
	}
	for _, link := range links {
		if len([]rune(link.Title)) > gitlabReleaseMaxNameRunes {
			t.Fatalf("title not capped: %d", len(link.Title))
		}
	}
}

func TestGitLabReleaseHookUpdatesLastWebhookAt(t *testing.T) {
	e := newGitLabReleaseHookEnv(t)
	e.pipeline(scheduledReleasePipeline)
	lastWebhook := func() *time.Time {
		data, _ := e.repository.BootstrapFor(e.bootstrap.Workspace.URLKey)
		for i := range data.IntegrationConnections {
			if data.IntegrationConnections[i].Provider == "gitlab" {
				return data.IntegrationConnections[i].LastWebhookAt
			}
		}
		t.Fatal("no GitLab connection")
		return nil
	}
	if lastWebhook() != nil {
		t.Fatal("lastWebhookAt set before any delivery")
	}
	e.deliver("", "Release Hook", "wrong", gitlabAssetsPayload("create", "v4.0.0"), http.StatusUnauthorized)
	if lastWebhook() != nil {
		t.Fatal("a rejected signature updated lastWebhookAt")
	}
	e.release("", gitlabAssetsPayload("create", "v4.0.0"), http.StatusAccepted)
	first := lastWebhook()
	if first == nil || time.Since(*first) > time.Minute {
		t.Fatalf("lastWebhookAt after release hook = %v", first)
	}
	e.release("", gitlabReleasePayload("delete", "v4.0.0", "x", ""), http.StatusAccepted)
	if second := lastWebhook(); second == nil || second.Before(*first) {
		t.Fatalf("accepted-but-ignored delivery did not refresh lastWebhookAt: %v", second)
	}
}
