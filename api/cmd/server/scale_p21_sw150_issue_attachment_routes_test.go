package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW150Case struct {
	name, kind, method, suffix, pattern string
	actor                               domain.User
	key                                 *domain.APIKey
	body                                map[string]any
	payload                             any
	alias, noEmbed, created             bool
	gate                                bool
	cycleAutomation                     bool
	stale, eventType                    string
	status, events                      int
}

func scaleP21SW150Normalize(t *testing.T, outcome scaleP21SW59Outcome, before scaleP21SW59Rows, attachment domain.Attachment, generated bool, documentID string, started, finished time.Time) any {
	t.Helper()
	result := scaleP21SW59Normalized(t, outcome, before, started, finished)
	if generated && attachment.ID == "" {
		t.Fatal("created attachment has no generated ID")
	}
	if documentID != "" {
		parts := strings.Split(strings.TrimPrefix(documentID, "document_content_collab_"), "_")
		if !strings.HasPrefix(documentID, "document_content_collab_") || len(parts) != 2 || len(parts[1]) != 16 {
			t.Fatal("embedded description has an unexpected collaboration generation ID", documentID)
		}
		stamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || time.Unix(0, stamp).Before(started) || time.Unix(0, stamp).After(finished) {
			t.Fatal("new collaboration generation ID is outside the measured operation", documentID)
		}
	}
	var visit func(any) any
	visit = func(value any) any {
		switch value := value.(type) {
		case string:
			// The same generated ID occurs in its filename, markdown embed,
			// structured content, SQL record and publication envelopes.
			if generated {
				value = strings.ReplaceAll(value, attachment.ID, "<generated-attachment>")
			}
			if documentID != "" {
				value = strings.ReplaceAll(value, documentID, "<generated-collaboration-document>")
			}
			return value
		case map[string]any:
			for key, child := range value {
				value[key] = visit(child)
			}
		case []any:
			for index, child := range value {
				value[index] = visit(child)
			}
		}
		return value
	}
	return visit(result)
}

func scaleP21SW150Multipart(t *testing.T, request *http.Request, noEmbed bool) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="shot.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("\x89PNG\r\n\x1a\nSW150 local image")); err != nil {
		t.Fatal(err)
	}
	if noEmbed {
		if err := writer.WriteField("embed", "false"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	request.ContentLength = int64(body.Len())
	request.Header.Set("Content-Type", writer.FormDataContentType())
}

func TestScaleP21SW150IssueAttachmentRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nSW150 fetched image"))
		case "/notes.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "SW150 fetched text")
		default:
			http.NotFound(w, r)
		}
	}))
	defer local.Close()
	previousFetch := attachmentFetchClient
	attachmentFetchClient = func() *http.Client { return local.Client() }
	defer func() { attachmentFetchClient = previousFetch }()

	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, member, guest := f.principals[0].user, f.principals[2].user, f.principals[3].user
	tokens := map[string]string{}
	for _, actor := range []domain.User{owner, member, guest} {
		_, token, err := repo.Login(t.Context(), actor.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[actor.ID] = token
	}
	memberVia := member
	prefix := idgen.New("sw150-")
	memberVia.Via = &domain.ActorBot{ID: prefix + "app", Type: domain.ActorBotTypeOAuthApplication, Name: "Local attachment tool", AvatarURL: local.URL + "/icon.png"}
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW150 rich attachment target", "teamId": f.public}, workspace, http.StatusCreated)
	stamp := time.Now().UTC().Add(-time.Hour)
	existingID, existingURL := prefix+"existing", local.URL+"/existing"
	const description = "Original **rich** description.\n"
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw150_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].ID != issue.ID {
				continue
			}
			target := &data.Issues[index]
			target.Version, target.UpdatedAt = 42, stamp
			target.Description, target.DescriptionState = description, "preserved-rich-state"
			target.DocumentContent = &domain.DocumentContent{
				ID: prefix + "rich-document", Version: 7, Content: description, ContentState: "preserved-rich-state", UpdatedAt: stamp,
				ContentData: map[string]any{"type": "doc", "retained": "Exact rich issue content", "content": []any{
					map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Original rich description."}}},
				}},
			}
			target.Permissions = []domain.IssuePermission{
				{ID: prefix + "owner-grant", IssueID: issue.ID, SubjectType: "user", SubjectID: owner.ID, Role: "editor", CreatedAt: stamp, UpdatedAt: stamp},
				{ID: prefix + "member-grant", IssueID: issue.ID, SubjectType: "user", SubjectID: member.ID, Role: "editor", CreatedAt: stamp, UpdatedAt: stamp},
				{ID: prefix + "guest-grant", IssueID: issue.ID, SubjectType: "user", SubjectID: guest.ID, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp},
			}
			target.Attachments = []domain.Attachment{{
				ID: existingID, IssueID: issue.ID, Title: "Stored link", Subtitle: "Original subtitle",
				URL: existingURL, ContentType: "text/uri-list", CreatedAt: stamp, UpdatedAt: &stamp, Creator: owner,
				Metadata: map[string]any{"keep": "original", "nested": map[string]any{"value": true}},
			}}
		}
		data.Comments[issue.ID] = []domain.Comment{{ID: prefix + "comment", Body: "Keep unrelated comments", User: member, CreatedAt: stamp}}
		data.Activities[issue.ID] = []domain.ActivityEvent{}
		data.Documents = append(data.Documents, domain.Document{
			ID: prefix + "earlier-document", Title: "Earlier aggregate witness",
			Content: "Keep unrelated document body", ContentData: map[string]any{"id": issue.ID, "preserved": "Earlier aggregate object"},
			Creator: owner, TeamIDs: []string{f.public}, CreatedAt: stamp, UpdatedAt: stamp,
		})
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("attachment fixture workspace missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	originalIssue, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw150_reset", "", nil, func(next *domain.Bootstrap) error {
			*next = data
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	createdLink := map[string]any{"url": local.URL + "/spec", "title": " Spec ", "subtitle": " New ", "metadata": map[string]any{"priority": "high"}}
	updatedLink := map[string]any{"url": existingURL, "subtitle": " Updated ", "iconUrl": ""}
	patchedLink := map[string]any{"title": " Patched ", "metadata": map[string]any{"changed": true}}
	noopLink := map[string]any{"title": "Stored link"}
	currentCycleID := prefix + "affected-current-cycle"
	cases := []scaleP21SW150Case{
		{name: "file-default-embed", kind: "file", actor: owner, suffix: "/attachments", pattern: "/attachments", method: http.MethodPost, created: true, status: http.StatusCreated, events: 1, payload: map[string]string{"name": "shot.png"}},
		{name: "file-embed-affected-team-current-cycle", kind: "file", cycleAutomation: true, actor: owner, suffix: "/attachments", pattern: "/attachments", method: http.MethodPost, created: true, status: http.StatusCreated, events: 1, payload: map[string]string{"name": "shot.png"}},
		{name: "file-disabled-embed", kind: "file", actor: member, noEmbed: true, suffix: "/attachments", pattern: "/attachments", method: http.MethodPost, created: true, status: http.StatusCreated, events: 1, payload: map[string]string{"name": "shot.png"}},
		{name: "file-alias-attribution", kind: "file", actor: memberVia, alias: true, suffix: "/attachments", pattern: "/attachments", method: http.MethodPost, created: true, status: http.StatusCreated, events: 1, payload: map[string]string{"name": "shot.png"}},
		{name: "url-default-embed", kind: "url", actor: owner, suffix: "/attachments/from-url", pattern: "/attachments/from-url", method: http.MethodPost, body: map[string]any{"url": local.URL + "/image.png"}, created: true, status: http.StatusCreated, events: 1, payload: map[string]string{"name": "image.png", "sourceUrl": local.URL + "/image.png"}},
		{name: "url-disabled-embed", kind: "url", actor: member, noEmbed: true, suffix: "/attachments/from-url", pattern: "/attachments/from-url", method: http.MethodPost, body: map[string]any{"url": "  " + local.URL + "/notes.txt  ", "title": "../report.txt", "embed": false}, created: true, status: http.StatusCreated, events: 1, payload: map[string]string{"name": "report.txt", "sourceUrl": local.URL + "/notes.txt"}},
		{name: "save-link-create", kind: "save", actor: owner, suffix: "/link-attachments", pattern: "/link-attachments", method: http.MethodPost, body: createdLink, created: true, status: http.StatusCreated, events: 1, payload: map[string]any{"url": local.URL + "/spec", "title": "Spec", "subtitle": "New", "metadata": map[string]any{"priority": "high"}}},
		{name: "save-link-existing", kind: "save", actor: member, suffix: "/link-attachments", pattern: "/link-attachments", method: http.MethodPost, body: updatedLink, status: http.StatusOK, events: 1, payload: map[string]any{"url": existingURL, "subtitle": "Updated", "iconUrl": ""}},
		{name: "save-stale-existing-now-creates", kind: "save", stale: "create", eventType: "attachment.updated", actor: owner, suffix: "/link-attachments", pattern: "/link-attachments", method: http.MethodPost, body: updatedLink, created: true, status: http.StatusCreated, events: 1, payload: map[string]any{"url": existingURL, "subtitle": "Updated", "iconUrl": ""}},
		{name: "save-stale-absent-now-updates", kind: "save", stale: "update", eventType: "attachment.created", actor: member, suffix: "/link-attachments", pattern: "/link-attachments", method: http.MethodPost, body: updatedLink, status: http.StatusOK, events: 1, payload: map[string]any{"url": existingURL, "subtitle": "Updated", "iconUrl": ""}},
		{name: "patch-link", kind: "patch", actor: owner, suffix: "/attachments/" + existingID, pattern: "/attachments/{attachmentId}", method: http.MethodPatch, body: patchedLink, status: http.StatusOK, events: 1, payload: map[string]any{"url": "", "title": "Patched", "metadata": map[string]any{"changed": true}}},
		{name: "patch-link-noop", kind: "patch", actor: member, suffix: "/attachments/" + existingID, pattern: "/attachments/{attachmentId}", method: http.MethodPatch, body: noopLink, status: http.StatusOK},
		{name: "patch-link-missing", kind: "patch", actor: owner, suffix: "/attachments/missing", pattern: "/attachments/{attachmentId}", method: http.MethodPatch, body: patchedLink, status: http.StatusNotFound},
		{name: "issue-link-original-payload", kind: "link", actor: owner, suffix: "/links", pattern: "/links", method: http.MethodPost, body: map[string]any{"url": "  " + local.URL + "/plain  ", "title": "  Plain link  "}, created: true, status: http.StatusCreated, events: 1, payload: map[string]any{"url": "  " + local.URL + "/plain  ", "title": "  Plain link  "}},
		{name: "issue-link-default-title", kind: "link", actor: member, suffix: "/links", pattern: "/links", method: http.MethodPost, body: map[string]any{"url": local.URL + "/plain"}, created: true, status: http.StatusCreated, events: 1, payload: map[string]any{"url": local.URL + "/plain"}},
		{name: "guest-permission-refused", kind: "file", actor: guest, suffix: "/attachments", pattern: "/attachments", method: http.MethodPost},
		{name: "selected-empty-refused", kind: "save", actor: member, key: &domain.APIKey{ID: prefix + "selected-empty", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}}, suffix: "/link-attachments", pattern: "/link-attachments", method: http.MethodPost, body: createdLink},
		{name: "issue-link-invalid-url", kind: "link", actor: owner, suffix: "/links", pattern: "/links", method: http.MethodPost, body: map[string]any{"url": "file:///private"}, status: http.StatusBadRequest},
		{name: "url-fetch-refused", kind: "url", actor: owner, suffix: "/attachments/from-url", pattern: "/attachments/from-url", method: http.MethodPost, body: map[string]any{"url": local.URL + "/missing"}, status: http.StatusBadGateway},
		{name: "literal-middleware-existing-gate", kind: "file", gate: true, actor: owner, suffix: "/attachments", pattern: "/attachments", method: http.MethodPost, status: http.StatusForbidden},
	}
	pairs := 0
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var outcomes []any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				reset(t)
				if test.cycleAutomation {
					restore := store.ForceFullMutationsForTesting(true)
					err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw150_cycle_fixture", issue.ID, nil, func(data *domain.Bootstrap) error {
						target, err := issueByID(data, issue.ID)
						if err != nil {
							return err
						}
						dueDate := stamp.Format("2006-01-02")
						target.DueDate, target.CycleID = &dueDate, nil
						if data.CycleSettings == nil {
							data.CycleSettings = map[string]domain.CycleSettings{}
						}
						data.CycleSettings[f.public] = domain.CycleSettings{Enabled: true, AutoAddDueDate: true}
						for index := range data.Cycles {
							if data.Cycles[index].TeamID == f.public && data.Cycles[index].Status == "current" {
								data.Cycles[index].Status = "completed"
							}
						}
						// Earlier decoys require both affected-team and current-state
						// selection; the due-date policy also needs exact cycle dates.
						data.Cycles = append([]domain.Cycle{
							{ID: prefix + "unrelated-current-cycle", TeamID: f.data.Teams[0].ID, Status: "current", StartsAt: stamp.Add(-24 * time.Hour), EndsAt: stamp.Add(24 * time.Hour)},
							{ID: prefix + "affected-upcoming-cycle", TeamID: f.public, Status: "upcoming", StartsAt: stamp.Add(24 * time.Hour), EndsAt: stamp.Add(72 * time.Hour)},
							{ID: currentCycleID, TeamID: f.public, Status: "current", StartsAt: stamp.Add(-24 * time.Hour), EndsAt: stamp.Add(24 * time.Hour)},
						}, data.Cycles...)
						return nil
					})
					restore()
					if err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
				}
				staleIssue := originalIssue
				peerAttachments := func(attachments []domain.Attachment) {
					t.Helper()
					restore := store.ForceFullMutationsForTesting(true)
					err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw150_peer_change", issue.ID, nil, func(data *domain.Bootstrap) error {
						target, err := issueByID(data, issue.ID)
						if err == nil {
							target.Attachments = attachments
						}
						return err
					})
					restore()
					if err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
				}
				if test.stale != "" {
					if test.stale == "update" {
						peerAttachments(nil)
					}
					func() {
						prior := f.literalRequest(test.actor, test.key, nil)
						prior.URL.Path = "/api/issue-records/" + issue.ID + "/link-attachments"
						prior, closeRead := beginRequestResourceReads(prior)
						defer closeRead()
						service := &server{store: source}
						data, query, err := service.publicIssueQuery(prior)
						if err != nil {
							t.Fatal(err)
						}
						target, err := source.AuthorizedIssueRecord(prior.Context(), query, issue.ID)
						if err != nil || issuePermissionRank(issueRole(service, data, target)) < issuePermissionRank("editor") {
							t.Fatal("stale save-helper fixture did not start with an authorized editor read", err)
						}
						staleIssue = target
					}()
					// Model another authorized writer committing after the read.
					if test.stale == "create" {
						peerAttachments(nil)
					} else {
						peerAttachments(originalIssue.Attachments)
					}
				}
				before := scaleP21SW59SQL(t, f)
				documents := scaleP21M3Fingerprint(t, repo, workspace, "documents")
				cycles := scaleP21M3Fingerprint(t, repo, workspace, "cycles")
				prior, err := repo.Events(t.Context(), issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				priorIDs := map[string]bool{}
				for _, event := range prior {
					priorIDs[event.ID] = true
				}
				got := scaleP21SW59Outcome{}
				checkPublication := func() {
					if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
						t.Error("attachment publication lost owned mutation output")
					}
				}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					checkPublication()
					got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
						"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
						"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot, "createdAt": event.CreatedAt,
					}))
				})
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					checkPublication()
					got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
						"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
						"seq": event.Seq, "clientId": event.ClientID, "payload": event.Payload, "createdAt": event.CreatedAt,
					}))
				})
				alias := test.alias || !test.gate && (test.kind == "file" || test.kind == "url")
				family := "issues"
				if alias {
					family = "issue-records"
				}
				request := f.literalRequest(test.actor, test.key, test.body)
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[test.actor.ID]})
				request.Method, request.URL.Path = test.method, "/api/"+family+"/"+issue.ID+test.suffix
				request.Header.Set("Content-Type", "application/json")
				if test.kind == "file" {
					scaleP21SW150Multipart(t, request, test.noEmbed)
				}
				service := &server{store: source, uploadPath: t.TempDir()}
				started := time.Now().UTC()
				func() {
					request, closeOutput := beginRequestResourceReads(request)
					defer closeOutput()
					call := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if !alias && !test.gate {
							// Native outer middleware currently rejects this literal
							// fixture. Authorize the isolated handler with the existing
							// issue-data and editor policy, without adding writer scope.
							data, _, err := service.publicIssueQuery(r)
							if err != nil {
								issueRecordsError(w, err)
								return
							}
							target, _, ok := service.authorizedPublicIssue(w, r, issue.ID)
							if !ok {
								return
							}
							if issuePermissionRank(issueRole(service, data, target)) < issuePermissionRank("editor") {
								writeError(w, http.StatusForbidden, "Issue editor permission required")
								return
							}
						}
						switch test.kind {
						case "file":
							service.createAttachment(w, r)
						case "url":
							service.createAttachmentFromURL(w, r)
						case "save":
							if test.stale == "" {
								service.upsertLinkAttachment(w, r)
								break
							}
							var input linkAttachmentInput
							if !decodeJSON(w, r, &input) {
								return
							}
							if err := validateLinkAttachmentInput(&input, true); err != nil {
								t.Fatal(err)
							}
							attachment, created, err := service.saveLinkAttachment(r.Context(), r, workspace, staleIssue, input)
							if created != test.created {
								t.Fatal("stale save-helper returned the wrong created result", created)
							}
							respondMutation(w, err, test.status, attachment)
						case "patch":
							service.updateLinkAttachment(w, r)
						case "link":
							if store.UsesIssueRecordMutations(r.Context()) {
								t.Fatal("ordinary issue-link route unexpectedly selected a scoped writer")
							}
							service.createIssueLink(w, r)
						}
					})
					if alias {
						call = service.issueRecordAlias(call)
					}
					mux := http.NewServeMux()
					mux.Handle(test.method+" /api/"+family+"/{id}"+test.pattern, call)
					response := &scaleP21SW59ResponseWriter{ResponseRecorder: httptest.NewRecorder(), t: t, source: source, requireHeld: test.status > 0 && test.status < 400}
					if !test.gate && !alias || service.authorizeWorkspaceRequest(response, request, test.actor) {
						mux.ServeHTTP(response, request)
					}
					got.Code, got.Header, got.Response = response.Code, response.Header().Clone(), bytes.Clone(response.Body.Bytes())
				}()
				finished := time.Now().UTC()
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				got.Rows = scaleP21SW59SQL(t, f)
				after, err := repo.Events(t.Context(), issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range after {
					if !priorIDs[event.ID] {
						got.Events = append(got.Events, event)
					}
				}
				if test.status > 0 && got.Code != test.status || test.status == 0 && got.Code < 400 {
					t.Fatalf("attachment status=%d want=%d body=%s", got.Code, test.status, got.Response)
				}
				if len(got.Events) != test.events || len(got.Webhooks) != test.events || len(got.Realtime) != test.events {
					t.Fatal("attachment route changed event/webhook/realtime count", len(got.Events), len(got.Webhooks), len(got.Realtime), test.events)
				}
				var attachment domain.Attachment
				if got.Code < 400 && json.Unmarshal(got.Response, &attachment) != nil {
					t.Fatal("attachment response is not a native attachment")
				}
				if got.Code < 400 {
					if test.created && attachment.Creator.ID != test.actor.ID {
						t.Fatal("created attachment lost its request actor", attachment.Creator.ID, test.actor.ID)
					}
					if test.created && test.actor.Via != nil && !reflect.DeepEqual(attachment.BotActor, test.actor.Via) {
						t.Fatal("created attachment lost request-scoped application attribution")
					}
					if !test.created && (attachment.ID != existingID || attachment.Creator.ID != owner.ID) {
						t.Fatal("updated attachment changed its stored identity or original creator")
					}
				}
				if test.events == 0 {
					if !reflect.DeepEqual(got.Rows, before) {
						t.Fatal("refused or unchanged attachment route changed business rows")
					}
				} else {
					expectedType := "attachment.created"
					if test.kind == "link" {
						expectedType = "issue.link_created"
					} else if !test.created {
						expectedType = "attachment.updated"
					}
					if test.eventType != "" {
						expectedType = test.eventType
					}
					if got.Events[0].Type != expectedType || got.Events[0].AggregateID != issue.ID ||
						!reflect.DeepEqual(scaleP21SW10JSON(t, got.Events[0].Payload), scaleP21SW10JSON(t, test.payload)) {
						t.Fatal("attachment mutation changed its original event type/aggregate/payload", got.Events[0])
					}
				}
				current, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(current.Permissions, originalIssue.Permissions) {
					t.Fatal("attachment route changed complete issue permissions")
				}
				if test.cycleAutomation && (current.CycleID == nil || *current.CycleID != currentCycleID ||
					current.DueDate == nil || *current.DueDate != stamp.Format("2006-01-02")) {
					t.Fatal("markdown embed did not preserve the due date and select the affected team's current cycle", current.CycleID, current.DueDate)
				}
				if got.Code < 400 {
					expectedCount := 1
					if test.created && test.stale != "create" {
						expectedCount++
					}
					found := false
					for _, saved := range current.Attachments {
						if saved.ID == attachment.ID {
							found = reflect.DeepEqual(saved, attachment)
						}
					}
					if len(current.Attachments) != expectedCount || !found {
						t.Fatal("saved attachment count or response identity differs from the canonical issue", len(current.Attachments), expectedCount)
					}
					if test.stale == "create" && (attachment.URL != existingURL || attachment.Subtitle != "Updated" || attachment.Metadata != nil || attachment.ID == existingID) {
						t.Fatal("stale existing save did not create exactly the requested new link", attachment)
					}
					if test.stale == "update" && (attachment.Title != "Stored link" || attachment.Subtitle != "Updated" ||
						!attachment.CreatedAt.Equal(stamp) || !reflect.DeepEqual(attachment.Metadata, originalIssue.Attachments[0].Metadata)) {
						t.Fatal("stale absent save did not preserve the current link while updating requested fields", attachment)
					}
				}
				embedded := test.events > 0 && (test.kind == "file" || test.kind == "url") && !test.noEmbed
				if embedded {
					if !strings.HasPrefix(current.Description, description) || !strings.Contains(current.Description, attachment.URL) ||
						current.DocumentContent == nil || current.DocumentContent.Content != current.Description || current.DocumentContent.Version != originalIssue.DocumentContent.Version+1 ||
						current.DescriptionState != "" || current.DocumentContent.ContentState != "" {
						t.Fatal("default attachment embed lost the existing description or rich-document synchronization")
					}
				} else if current.Description != originalIssue.Description || current.DescriptionState != originalIssue.DescriptionState ||
					!reflect.DeepEqual(current.DocumentContent, originalIssue.DocumentContent) {
					t.Fatal("non-embed attachment path rewrote existing rich/document content")
				}
				files, err := os.ReadDir(service.uploadPath)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				fileCreated := got.Code < 400 && (test.kind == "file" || test.kind == "url")
				if len(files) != 0 && !fileCreated || fileCreated && len(files) != 1 {
					t.Fatal("attachment route retained an unexpected uploaded file count", len(files), fileCreated)
				}
				if fileCreated {
					storage, err := service.storage()
					if err != nil {
						t.Fatal(err)
					}
					reader, _, _, err := storage.Open(t.Context(), strings.TrimPrefix(attachment.URL, "/uploads/"))
					if err != nil {
						t.Fatal(err)
					}
					content, readErr := io.ReadAll(reader)
					_ = reader.Close()
					expected := "\x89PNG\r\n\x1a\nSW150 local image"
					if test.kind == "url" {
						expected = "\x89PNG\r\n\x1a\nSW150 fetched image"
						if test.noEmbed {
							expected = "SW150 fetched text"
						}
					}
					if readErr != nil || string(content) != expected || attachment.Size != int64(len(expected)) {
						t.Fatal("stored attachment bytes or size changed", readErr, attachment.Size)
					}
				}
				if scaleP21M3Fingerprint(t, repo, workspace, "documents") != documents {
					t.Fatal("attachment route changed the unrelated earlier aggregate witness")
				}
				if scaleP21M3Fingerprint(t, repo, workspace, "cycles") != cycles {
					t.Fatal("attachment route changed cycle metadata while applying issue cycle automation")
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("attachment route retained work or decoded complete bounded metadata")
				}
				documentID := ""
				if embedded {
					documentID = current.DocumentContent.ID
					if documentID == originalIssue.DocumentContent.ID {
						t.Fatal("embedded description retained its previous collaboration generation")
					}
				}
				outcomes = append(outcomes, scaleP21SW150Normalize(t, got, before, attachment, test.created && got.Code < 400, documentID, started, finished))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("actual attachment route/SQL/event/webhook/realtime differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
			pairs++
		})
	}
	t.Logf("attachment route/effect pairs=%d: file/URL use the full alias gate; native links/helpers use existing issue-data/editor authorization without added writer scope; literal native outer gate retains 403; all fetches use the local httptest origin", pairs)
}
