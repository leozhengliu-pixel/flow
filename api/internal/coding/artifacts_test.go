package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

func TestDetectArtifactType(t *testing.T) {
	for _, test := range []struct {
		name                         string
		head                         []byte
		contentType, kind, extension string
		ok                           bool
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png", ArtifactKindScreenshot, ".png", true},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F'}, "image/jpeg", ArtifactKindScreenshot, ".jpg", true},
		{"webm", append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x42, 0x82, 0x84}, []byte("webm")...), "video/webm", ArtifactKindVideo, ".webm", true},
		{"matroska", append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x88}, []byte("matroska")...), "", "", "", false},
		{"mp4", []byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00"), "video/mp4", ArtifactKindVideo, ".mp4", true},
		{"quicktime", []byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00"), "", "", "", false},
		{"gif", []byte("GIF89a"), "", "", "", false},
		{"svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), "", "", "", false},
		{"html", []byte("<!doctype html><script>"), "", "", "", false},
		{"empty", nil, "", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			contentType, kind, extension, ok := DetectArtifactType(test.head)
			if contentType != test.contentType || kind != test.kind || extension != test.extension || ok != test.ok {
				t.Fatalf("got %q %q %q %t", contentType, kind, extension, ok)
			}
		})
	}
}

func TestCleanArtifactText(t *testing.T) {
	for _, test := range []struct {
		in, want string
		limit    int
		ok       bool
	}{
		{"  before ", "before", 10, true},
		{"settings\n  page\tafter", "settings page after", 40, true},
		{strings.Repeat("é", 10), strings.Repeat("é", 10), 10, true},
		{strings.Repeat("a", 11), "", 10, false},
		{"", "", 10, true},
	} {
		got, ok := CleanArtifactText(test.in, test.limit)
		if got != test.want || ok != test.ok {
			t.Errorf("CleanArtifactText(%q) = %q %t", test.in, got, ok)
		}
	}
}

func artifactAt(id, kind, label, group string, minute int) domain.CodingArtifact {
	return domain.CodingArtifact{ID: id, Kind: kind, Label: label, Group: group, URL: "https://flow.example/api/files/signed/" + id, CreatedAt: time.Date(2026, 10, 18, 10, minute, 0, 0, time.UTC)}
}

func TestArtifactMarkdown(t *testing.T) {
	if got := ArtifactMarkdown(nil); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := ArtifactMarkdown([]domain.CodingArtifact{{ID: "x", Kind: ArtifactKindScreenshot, Label: "before"}}); got != "" {
		t.Fatalf("artifacts without URLs render nothing: %q", got)
	}
	markdown := ArtifactMarkdown([]domain.CodingArtifact{
		artifactAt("after1", ArtifactKindScreenshot, "after", "Settings page", 3),
		artifactAt("before1", ArtifactKindScreenshot, "before", "Settings page", 1),
		artifactAt("before0", ArtifactKindScreenshot, "before", "settings page", 0), // older capture of the same flow
		artifactAt("free", ArtifactKindScreenshot, "Empty state [mobile] | dark", "", 4),
		artifactAt("onlybefore", ArtifactKindScreenshot, "before", "Login", 5),
		artifactAt("video", ArtifactKindVideo, "Checkout flow", "", 6),
		artifactAt("log", "log", "setup", "", 7),
	})
	for _, want := range []string{
		ArtifactSectionStart + "\n### Screenshots\n",
		"**Settings page**\n\n| Before | After |\n|---|---|\n| ![Settings page before](https://flow.example/api/files/signed/before1) | ![Settings page after](https://flow.example/api/files/signed/after1) |",
		"**Login**\n\n| Before | After |\n|---|---|\n| ![Login before](https://flow.example/api/files/signed/onlybefore) | _none_ |",
		"![Empty state (mobile) / dark](https://flow.example/api/files/signed/free)",
		"Recordings:\n\n- [Checkout flow](https://flow.example/api/files/signed/video)",
		ArtifactSectionEnd,
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown lacks %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "before0") || strings.Contains(markdown, "/log") {
		t.Errorf("superseded captures and logs must not render:\n%s", markdown)
	}
	if strings.Index(markdown, "Settings page") > strings.Index(markdown, "Login") {
		t.Errorf("pairs keep capture order:\n%s", markdown)
	}
}

func TestMergeArtifactSection(t *testing.T) {
	section := ArtifactSectionStart + "\nnew\n" + ArtifactSectionEnd
	old := ArtifactSectionStart + "\nold\n" + ArtifactSectionEnd
	for _, test := range []struct {
		name, body, section, want string
	}{
		{"append", "Summary\n\nFixes FLOW-1\n", section, "Summary\n\nFixes FLOW-1\n\n" + section},
		{"empty body", "", section, section},
		{"replace keeps surrounding text", "Summary\n\n" + old + "\n\n[Session](x)", section, "Summary\n\n" + section + "\n\n[Session](x)"},
		{"unchanged", "Summary\n\n" + section, section, "Summary\n\n" + section},
		{"remove in the middle", "Summary\n\n" + old + "\n\n[Session](x)", "", "Summary\n\n[Session](x)"},
		{"remove at the end", "Summary\n\n" + old + "\n", "", "Summary"},
		{"remove only section", old, "", ""},
		{"remove absent", "Summary", "", "Summary"},
		{"unterminated marker appends", "Summary " + ArtifactSectionStart, section, "Summary " + ArtifactSectionStart + "\n\n" + section},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := MergeArtifactSection(test.body, test.section); got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}

type fakeBodyHost struct {
	current  codehost.ChangeRequest
	getErr   error
	writeErr error
	writes   []string
}

func (h *fakeBodyHost) GetChangeRequest(context.Context, string, codehost.RepoRef, int) (codehost.ChangeRequest, error) {
	return h.current, h.getErr
}

func (h *fakeBodyHost) UpdateChangeRequestBody(_ context.Context, _ string, _ codehost.RepoRef, _ int, body string) error {
	if h.writeErr != nil {
		return h.writeErr
	}
	h.writes = append(h.writes, body)
	h.current.Body = body
	return nil
}

func TestSyncChangeRequestArtifacts(t *testing.T) {
	ctx := context.Background()
	repo := codehost.RepoRef{CodeHost: domain.CodeHostGitHub, HostName: "github.com", FullName: "acme/web"}
	artifacts := []domain.CodingArtifact{artifactAt("b", ArtifactKindScreenshot, "before", "", 0), artifactAt("a", ArtifactKindScreenshot, "after", "", 1)}

	host := &fakeBodyHost{current: codehost.ChangeRequest{State: "open", Body: "Summary\n\nFixes FLOW-7"}}
	changed, err := SyncChangeRequestArtifacts(ctx, host, "ws", repo, 7, artifacts)
	if err != nil || !changed || len(host.writes) != 1 || !strings.HasPrefix(host.writes[0], "Summary\n\nFixes FLOW-7\n\n"+ArtifactSectionStart) {
		t.Fatalf("first sync: %t %v %q", changed, err, host.writes)
	}
	// Idempotent: the same artifacts do not rewrite the body.
	if changed, err := SyncChangeRequestArtifacts(ctx, host, "ws", repo, 7, artifacts); err != nil || changed || len(host.writes) != 1 {
		t.Fatalf("second sync: %t %v %d", changed, err, len(host.writes))
	}
	// Closed or merged PRs keep their reviewed body.
	closed := &fakeBodyHost{current: codehost.ChangeRequest{State: "merged", Body: "Summary"}}
	if changed, err := SyncChangeRequestArtifacts(ctx, closed, "ws", repo, 7, artifacts); err != nil || changed || len(closed.writes) != 0 {
		t.Fatalf("merged: %t %v", changed, err)
	}
	transient := errors.Join(codehost.ErrTransient, errors.New("502"))
	if _, err := SyncChangeRequestArtifacts(ctx, &fakeBodyHost{getErr: transient}, "ws", repo, 7, artifacts); !errors.Is(err, codehost.ErrTransient) {
		t.Fatalf("get error: %v", err)
	}
	if _, err := SyncChangeRequestArtifacts(ctx, &fakeBodyHost{current: codehost.ChangeRequest{State: "open"}, writeErr: transient}, "ws", repo, 7, artifacts); !errors.Is(err, codehost.ErrTransient) {
		t.Fatalf("write error: %v", err)
	}
	if _, err := SyncChangeRequestArtifacts(ctx, nil, "ws", repo, 7, artifacts); err == nil {
		t.Fatal("nil host must fail")
	}
}

func TestBrowserGuidanceNamesTheRunnerTools(t *testing.T) {
	for _, tool := range []string{"browser_start_app", "browser_open", "browser_click", "browser_type", "browser_screenshot", "browser_record_start", "browser_record_stop", "attach_screenshot", "browser_stop_app"} {
		if !strings.Contains(BrowserGuidance, tool) {
			t.Errorf("guidance does not mention %s", tool)
		}
	}
	if !strings.Contains(BrowserGuidance, `"before"`) || !strings.Contains(BrowserGuidance, `"after"`) {
		t.Error("guidance must explain before/after labels")
	}
}
