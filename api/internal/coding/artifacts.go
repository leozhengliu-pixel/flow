package coding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// Browser testing and session artifacts (docs/coding-sessions-design.md §17).
// Owner: track I. The runner's browser MCP tools upload screenshots and
// recordings through POST /internal/coding-runtime/artifacts; this file holds
// the pieces other tracks consume: the guidance text (B's bootstrap), the
// PR/MR "Screenshots" section (D and J), and the upload validation rules.

// Artifact kinds.
const (
	ArtifactKindScreenshot = "screenshot"
	ArtifactKindVideo      = "video"
)

// Artifact labels with a meaning; anything else is free text.
const (
	ArtifactLabelBefore = "before"
	ArtifactLabelAfter  = "after"
)

// Artifact limits and URL lifetimes.
const (
	// MaxArtifactsPerTurn caps uploads per harness turn (design §17).
	MaxArtifactsPerTurn = 20
	// MaxArtifactsPerSession bounds storage for very long sessions.
	MaxArtifactsPerSession = 200
	// MaxArtifactLabelLength and MaxArtifactGroupLength bound the free-text fields.
	MaxArtifactLabelLength = 120
	MaxArtifactGroupLength = 80
	// ArtifactListURLLifetime is the lifetime of signed URLs in API listings.
	ArtifactListURLLifetime = time.Hour
	// ArtifactPullRequestURLLifetime is the lifetime of the signed URLs written
	// into PR/MR bodies. They are renewed whenever the body is rewritten.
	ArtifactPullRequestURLLifetime = 30 * 24 * time.Hour
)

// Markers delimit the section Flow owns inside a PR/MR body, so it can be
// rewritten without touching text a person added around it.
const (
	ArtifactSectionStart = "<!-- flow:screenshots:start -->"
	ArtifactSectionEnd   = "<!-- flow:screenshots:end -->"
)

// BrowserGuidance is appended to the bootstrap guidance by track B when
// browser testing is enabled (BootstrapBrowser.Enabled). It names the runner's
// MCP tools (sandbox-runner/src/mcp/tools/browser.ts).
const BrowserGuidance = `## Browser testing

You can run the app and test it in a headless Chromium browser with the flow MCP tools:
browser_start_app, browser_open, browser_click, browser_type, browser_press, browser_wait_for, browser_read,
browser_screenshot, browser_record_start, browser_record_stop, browser_close, browser_stop_app and attach_screenshot.

When your change affects anything a person sees in the app (pages, components, styles, copy, layout):
1. Start the app with browser_start_app. It uses the environment's guidance (FLOW_BROWSER_APP_COMMAND and
   FLOW_BROWSER_APP_URL), .flow/browser.json, or the dev/start script in package.json. Pass command and url
   yourself when the project documents a different way to run it.
2. Before you change code, take a "before" screenshot of each affected screen on the base ref:
   browser_screenshot with label "before" and a short group name per screen (for example group "settings-page").
   If you already changed code, check out the base ref in a separate git worktree (or git stash), start the app from
   it, take the "before" screenshots, then return to your branch.
3. After the change, walk through the same flow (browser_open, browser_click, browser_type, browser_read) and take
   the "after" screenshot with the same group name. Use browser_record_start/browser_record_stop to record a short
   video when the change is an interaction rather than a static screen.
4. Look at every screenshot you take. When something is broken (console errors, layout problems, wrong text), fix it
   and test again before you finish.
5. Screenshots are attached to the session and to the pull request automatically. At most 20 per turn; keep them
   focused. Never capture secrets or personal data.
6. Stop the app with browser_stop_app when you are done.
`

// ArtifactUploadError is a rejected upload with an HTTP status and a stable code.
type ArtifactUploadError struct {
	Status  int
	Code    string
	Message string
}

func (e *ArtifactUploadError) Error() string { return e.Message }

// ErrArtifactSessionClosed means the session cannot take artifacts any more.
var ErrArtifactSessionClosed = errors.New("coding: session does not accept artifacts")

// DetectArtifactType sniffs the first bytes of an upload and returns its
// content type, kind and file extension. Only PNG, JPEG, WebM and MP4 are
// accepted; the client's declared content type is never trusted.
func DetectArtifactType(head []byte) (contentType, kind, extension string, ok bool) {
	switch {
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", ArtifactKindScreenshot, ".png", true
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", ArtifactKindScreenshot, ".jpg", true
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		// EBML header: WebM or Matroska. Playwright records WebM.
		if bytes.Contains(head[:min(len(head), 64)], []byte("webm")) {
			return "video/webm", ArtifactKindVideo, ".webm", true
		}
		return "", "", "", false
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		brand := string(head[8:12])
		switch brand {
		case "isom", "iso2", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "dash", "M4V ":
			return "video/mp4", ArtifactKindVideo, ".mp4", true
		}
	}
	return "", "", "", false
}

// CleanArtifactText normalizes a label or group: single line, trimmed and
// bounded. It returns false when the text is too long.
func CleanArtifactText(value string, limit int) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) > limit {
		return "", false
	}
	return value, true
}

// IsBeforeAfter reports whether an artifact is half of a before/after pair.
func IsBeforeAfter(label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	return label == ArtifactLabelBefore || label == ArtifactLabelAfter
}

// ArtifactMarkdown renders the "Screenshots" section of a PR/MR body. URLs
// must already be absolute (signed Flow file URLs, ArtifactPullRequestURLLifetime).
// Before/after shots of the same group become a two-column table; other
// screenshots follow as images and recordings as links (code hosts do not
// embed external videos). It returns "" when there is nothing to show; the
// result is wrapped in ArtifactSectionStart/End so MergeArtifactSection can
// replace it later.
func ArtifactMarkdown(artifacts []domain.CodingArtifact) string {
	type pair struct {
		group         string
		before, after *domain.CodingArtifact
	}
	pairs := map[string]*pair{}
	var order []*pair
	var others, videos []domain.CodingArtifact
	sorted := append([]domain.CodingArtifact(nil), artifacts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })
	for index := range sorted {
		artifact := sorted[index]
		if artifact.URL == "" {
			continue
		}
		switch {
		case artifact.Kind == ArtifactKindVideo:
			videos = append(videos, artifact)
		case artifact.Kind == ArtifactKindScreenshot && IsBeforeAfter(artifact.Label):
			key := strings.ToLower(strings.TrimSpace(artifact.Group))
			current := pairs[key]
			if current == nil {
				current = &pair{}
				pairs[key] = current
				order = append(order, current)
			}
			// The latest capture (and its spelling of the group) wins when a
			// flow was re-tested.
			current.group = strings.TrimSpace(artifact.Group)
			if strings.EqualFold(strings.TrimSpace(artifact.Label), ArtifactLabelBefore) {
				current.before = &sorted[index]
			} else {
				current.after = &sorted[index]
			}
		case artifact.Kind == ArtifactKindScreenshot:
			others = append(others, artifact)
		}
	}
	if len(order) == 0 && len(others) == 0 && len(videos) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(ArtifactSectionStart + "\n### Screenshots\n\n")
	for _, current := range order {
		if current.group != "" {
			fmt.Fprintf(&out, "**%s**\n\n", markdownText(current.group))
		}
		out.WriteString("| Before | After |\n|---|---|\n")
		fmt.Fprintf(&out, "| %s | %s |\n\n", markdownImage(current.before, "Before"), markdownImage(current.after, "After"))
	}
	for index := range others {
		label := others[index].Label
		if label == "" {
			label = "Screenshot"
		}
		fmt.Fprintf(&out, "%s\n\n", markdownImage(&others[index], label))
	}
	if len(videos) > 0 {
		out.WriteString("Recordings:\n\n")
		for _, video := range videos {
			label := video.Label
			if label == "" {
				label = "Recording"
			}
			fmt.Fprintf(&out, "- [%s](%s)\n", markdownText(label), video.URL)
		}
		out.WriteString("\n")
	}
	out.WriteString("<sub>Captured by Flow in the coding session sandbox. Image links expire after 30 days.</sub>\n" + ArtifactSectionEnd)
	return out.String()
}

func markdownImage(artifact *domain.CodingArtifact, fallback string) string {
	if artifact == nil {
		return "_none_"
	}
	alt := artifact.Label
	if artifact.Group != "" {
		alt = artifact.Group + " " + artifact.Label
	}
	if strings.TrimSpace(alt) == "" {
		alt = fallback
	}
	return fmt.Sprintf("![%s](%s)", markdownText(alt), artifact.URL)
}

// markdownText removes characters that would break out of link text or a
// table cell.
func markdownText(value string) string {
	replacer := strings.NewReplacer("[", "(", "]", ")", "|", "/", "<", "‹", ">", "›", "\n", " ", "\r", " ", "`", "'", "*", "", "_", " ")
	return strings.TrimSpace(replacer.Replace(value))
}

// MergeArtifactSection replaces the Flow screenshots section in body, appends
// it when absent, and removes it when section is "". Text outside the
// markers is kept byte for byte.
func MergeArtifactSection(body, section string) string {
	start := strings.Index(body, ArtifactSectionStart)
	end := -1
	if start >= 0 {
		if offset := strings.Index(body[start:], ArtifactSectionEnd); offset >= 0 {
			end = start + offset + len(ArtifactSectionEnd)
		}
	}
	if start >= 0 && end >= 0 {
		before, after := body[:start], body[end:]
		if section == "" {
			return strings.TrimRight(before, "\n") + trimLeadingNewlines(after, before != "")
		}
		return before + section + after
	}
	if section == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return section
	}
	return strings.TrimRight(body, "\n") + "\n\n" + section
}

func trimLeadingNewlines(value string, keepSeparator bool) string {
	trimmed := strings.TrimLeft(value, "\n")
	if trimmed == "" {
		return ""
	}
	if keepSeparator {
		return "\n\n" + trimmed
	}
	return trimmed
}

// ChangeRequestBodyHost is the part of codehost.Host used to keep the
// screenshots section of a PR/MR current.
type ChangeRequestBodyHost interface {
	GetChangeRequest(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int) (codehost.ChangeRequest, error)
	UpdateChangeRequestBody(ctx context.Context, workspaceKey string, repo codehost.RepoRef, number int, body string) error
}

// SyncChangeRequestArtifacts rewrites the screenshots section of one PR/MR
// body through the code host contract (codehost.ChangeRequest.Body and
// Host.UpdateChangeRequestBody). It is idempotent: an unchanged body is not
// written. Artifact URLs must be absolute signed URLs.
func SyncChangeRequestArtifacts(ctx context.Context, host ChangeRequestBodyHost, workspaceKey string, repo codehost.RepoRef, number int, artifacts []domain.CodingArtifact) (bool, error) {
	if host == nil {
		return false, errors.New("coding: no code host for the change request")
	}
	current, err := host.GetChangeRequest(ctx, workspaceKey, repo, number)
	if err != nil {
		return false, err
	}
	if current.State != "" && current.State != "open" {
		// Merged or closed PRs keep the body they were reviewed with.
		return false, nil
	}
	next := MergeArtifactSection(current.Body, ArtifactMarkdown(artifacts))
	if next == current.Body {
		return false, nil
	}
	if err := host.UpdateChangeRequestBody(ctx, workspaceKey, repo, number, next); err != nil {
		return false, err
	}
	return true, nil
}
