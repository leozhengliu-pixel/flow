package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type providerAdapter struct {
	Name, BaseURL, Probe string
	Actions              []string
}

var providerAdapters = map[string]providerAdapter{
	"notion":          {Name: "Notion", BaseURL: "https://api.notion.com/v1", Probe: "/users/me", Actions: []string{"preview", "attach"}},
	"intercom":        {Name: "Intercom", BaseURL: "https://api.intercom.io", Probe: "/me", Actions: []string{"preview", "import"}},
	"sentry":          {Name: "Sentry", BaseURL: "https://sentry.io/api/0", Probe: "/", Actions: []string{"preview", "import"}},
	"figma":           {Name: "Figma", BaseURL: "https://api.figma.com/v1", Probe: "/me", Actions: []string{"preview", "attach"}},
	"google-calendar": {Name: "Google Calendar", BaseURL: "https://www.googleapis.com/calendar/v3", Probe: "/users/me/calendarList?maxResults=1", Actions: []string{"syncAvailability"}},
	"cursor":          {Name: "Cursor", BaseURL: "https://api.cursor.com/v1", Probe: "/agents?limit=1", Actions: []string{"launch", "status", "stop"}},
	"codex":           {Name: "Codex", Actions: []string{"launch", "status", "stop"}},
	"zapier":          {Name: "Zapier", Actions: []string{"createIssue", "updateIssue", "subscribe", "unsubscribe"}},
}

type providerActionInput struct {
	Action      string `json:"action"`
	ResourceID  string `json:"resourceId"`
	IssueID     string `json:"issueId"`
	TeamID      string `json:"teamId"`
	Repository  string `json:"repository"`
	Branch      string `json:"branch"`
	Prompt      string `json:"prompt"`
	Title       string `json:"title"`
	Description string `json:"description"`
	StateID     string `json:"stateId"`
	URL         string `json:"url"`
}
type providerResult struct {
	ContactEmail   string     `json:"-"`
	Identifier     string     `json:"identifier,omitempty"`
	ID             string     `json:"id,omitempty"`
	Title          string     `json:"title,omitempty"`
	Body           string     `json:"body,omitempty"`
	URL            string     `json:"url,omitempty"`
	IssueID        string     `json:"issueId,omitempty"`
	Status         string     `json:"status,omitempty"`
	AvailableUntil *time.Time `json:"availableUntil,omitempty"`
}
type providerSecret struct {
	Token   string `json:"token"`
	BaseURL string `json:"baseUrl"`
}

func providerEnv(provider string) string {
	return "FLOW_INTEGRATION_" + strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
}
func providerBase(provider string) string {
	return strings.TrimRight(firstNonEmpty(os.Getenv(providerEnv(provider)+"_API_URL"), providerAdapters[provider].BaseURL), "/")
}

func (s *server) providerJSON(ctx context.Context, connection domain.IntegrationConnection, workspace, method, path string, input any, explicitToken string) (map[string]any, error) {
	base := providerBase(connection.Provider)
	token := explicitToken
	if token == "" {
		token = firstNonEmpty(connection.OAuthAccessToken, os.Getenv(providerEnv(connection.Provider)+"_ACCESS_TOKEN"))
		if store.ConnectorSecretsConfigured() {
			raw, err := s.store.ReadConnectorSecret(ctx, connectorSecretID(workspace, "provider", connection.ID), false)
			if err == nil {
				var secret providerSecret
				if json.Unmarshal(raw, &secret) == nil && secret.BaseURL == base {
					token = secret.Token
				}
			}
		}
	}
	if token == "" {
		return nil, fmt.Errorf("Authorize %s before running this action", providerAdapters[connection.Provider].Name)
	}
	endpoint := base + path
	if !integrationEndpointSafe(ctx, endpoint, s.authDisabled) {
		return nil, fmt.Errorf("Invalid provider API endpoint")
	}
	var body io.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	switch connection.Provider {
	case "cursor":
		req.SetBasicAuth(token, "")
	case "figma":
		if explicitToken != "" || connection.OAuthAccessToken == "" {
			req.Header.Set("X-Figma-Token", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	default:
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if connection.Provider == "notion" {
		req.Header.Set("Notion-Version", "2025-09-03")
	}
	if connection.Provider == "intercom" {
		req.Header.Set("Intercom-Version", "2.14")
	}
	client := s.connectorHTTPClient()
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached", providerAdapters[connection.Provider].Name)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned HTTP %d", providerAdapters[connection.Provider].Name, res.StatusCode)
	}
	if res.StatusCode == 204 {
		return map[string]any{}, nil
	}
	var result map[string]any
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&result) != nil {
		return nil, fmt.Errorf("Invalid provider response")
	}
	return result, nil
}

func (s *server) configureProvider(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	r = r.WithContext(context.WithValue(r.Context(), workspaceKeyContextKey{}, data.Workspace.URLKey))
	if !s.applicationPolicyAdmin(r, data) {
		writeError(w, 403, "Workspace administrator required")
		return
	}
	provider := r.PathValue("provider")
	adapter, ok := providerAdapters[provider]
	if !ok {
		writeError(w, 400, "Unsupported provider")
		return
	}
	var input struct {
		Token  string `json:"token"`
		Name   string `json:"name"`
		TeamID string `json:"teamId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if data.WorkspaceSettings.HIPAACompliance {
		writeError(w, 403, "External processing is disabled")
		return
	}
	connection := domain.IntegrationConnection{Provider: provider, Config: map[string]string{}, Name: firstNonEmpty(strings.TrimSpace(input.Name), adapter.Name), ConnectedBy: data.Viewer.ID, Status: "configured", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Scopes: []string{}, Channels: []string{}}
	for _, current := range data.IntegrationConnections {
		if current.Provider == provider {
			connection = current
			break
		}
	}
	if connection.ID == "" {
		id, err := randomSecret("integration_")
		if err != nil {
			writeError(w, 500, "Could not configure provider")
			return
		}
		connection.ID = id
	}
	connection.ConnectedBy = data.Viewer.ID
	if input.TeamID != "" {
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == input.TeamID }) {
			writeError(w, 400, "Invalid team")
			return
		}
		if connection.Config == nil {
			connection.Config = map[string]string{}
		}
		connection.Config["teamId"] = input.TeamID
	}
	if input.Token != "" && !store.ConnectorSecretsConfigured() {
		writeError(w, 503, "Configure FLOW_CONNECTOR_SECRET_KEY before storing provider tokens")
		return
	}
	if provider == "zapier" {
		// Zapier authenticates requests to Flow with a scoped Flow API key.
		connection.Status = "configured"
	} else if provider == "codex" {
		if err := s.probeCodex(r.Context()); err != nil {
			writeError(w, 502, err.Error())
			return
		}
		connection.Status = "connected"
	} else {
		identity, err := s.providerJSON(r.Context(), connection, workspaceKey(r), "GET", adapter.Probe, nil, input.Token)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		valid := identity["id"] != nil
		switch provider {
		case "sentry":
			valid = identity["auth"] != nil
		case "cursor":
			_, valid = identity["items"]
		case "google-calendar":
			valid = identity["kind"] == "calendar#calendarList"
		}
		if !valid {
			writeError(w, 502, "Provider did not confirm an authenticated account")
			return
		}
		connection.Status = "connected"
	}
	if input.Token != "" {
		raw, _ := json.Marshal(providerSecret{Token: input.Token, BaseURL: providerBase(provider)})
		if err := s.store.PutConnectorSecret(r.Context(), connectorSecretID(workspaceKey(r), "provider", connection.ID), raw, time.Now().AddDate(1, 0, 0)); err != nil {
			writeError(w, 500, "Could not store provider credentials")
			return
		}
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.adapter_configured", connection.ID, nil, func(next *domain.Bootstrap) error {
		index := slices.IndexFunc(next.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == connection.ID })
		if index < 0 {
			next.IntegrationConnections = append(next.IntegrationConnections, connection)
		} else {
			next.IntegrationConnections[index] = connection
		}
		return nil
	})
	respondMutation(w, err, 200, redactIntegrationConnection(connection))
}

func (s *server) providerAction(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var input providerActionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	provider := r.PathValue("provider")
	adapter, ok := providerAdapters[provider]
	if !ok || !slices.Contains(adapter.Actions, input.Action) {
		writeError(w, 400, "Unsupported provider action")
		return
	}
	data := s.workspaceData(r)
	r = r.WithContext(context.WithValue(r.Context(), workspaceKeyContextKey{}, data.Workspace.URLKey))
	if data.WorkspaceSettings.HIPAACompliance {
		writeError(w, 403, "External processing is disabled")
		return
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		required := "write"
		if input.Action == "subscribe" || input.Action == "unsubscribe" {
			required = "admin"
		}
		if !apiKeyHasScope(key, required) {
			writeError(w, 403, "API key lacks the required scope")
			return
		}
	}
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
		return item.Provider == provider && (item.Status == "connected" || provider == "zapier" && item.Status == "configured")
	})
	if index < 0 {
		writeError(w, 409, "Connect this integration first")
		return
	}
	connection := data.IntegrationConnections[index]
	if input.IssueID != "" {
		issue, err := s.providerIssue(r, input.IssueID)
		if err != nil {
			writeError(w, 404, "Issue is not accessible")
			return
		}
		input.IssueID = issue.ID
	}
	if input.Action == "launch" && len(data.ProviderJobs) >= 1000 {
		writeError(w, 409, "Coding run limit reached")
		return
	}
	if provider == "codex" || provider == "cursor" {
		if agentWorkspacePolicy(data.WorkspaceSettings, data.ViewerRole) != nil {
			writeError(w, 403, "Agent access is disabled by workspace policy")
			return
		}
	}
	if input.Action != "preview" && input.Action != "status" {
		zapIssueAction := provider == "zapier" && (input.Action == "createIssue" || input.Action == "updateIssue")
		if !zapIssueAction && !s.applicationPolicyAdmin(r, data) {
			writeError(w, 403, "Workspace administrator required for integration actions")
			return
		}
	}
	var result providerResult
	var err error
	switch provider {
	case "codex":
		result, err = s.codexProviderAction(r, data, connection, input)
	case "cursor":
		result, err = s.cursorProviderAction(r, data, connection, input)
	case "google-calendar":
		result, err = s.calendarProviderAction(r, data, connection)
	case "zapier":
		result, err = s.zapierProviderAction(r, data, connection, input)
	default:
		result, err = s.readProviderResource(r, connection, input.ResourceID)
		if err == nil && input.Action == "attach" {
			err = s.attachProviderResource(r, data, result, input.IssueID)
		}
		if err == nil && input.Action == "import" {
			result.IssueID, err = s.importProviderResource(r, data, connection, result, input.TeamID)
		}
	}
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	if result.IssueID != "" {
		if issue, err := s.store.IssueRecord(r.Context(), workspaceKey(r), result.IssueID); err == nil {
			result.Identifier = issue.Identifier
		}
	}
	writeJSON(w, 200, result)
}

func resourceID(raw string, provider string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if provider == "sentry" {
			for i, p := range parts {
				if p == "issues" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
		if provider == "figma" && len(parts) > 1 {
			return parts[1]
		}
		return parts[len(parts)-1]
	}
	return strings.TrimSpace(raw)
}

func (s *server) readProviderResource(r *http.Request, connection domain.IntegrationConnection, rawID string) (providerResult, error) {
	id := resourceID(rawID, connection.Provider)
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "/?#\\") {
		return providerResult{}, errInvalid
	}
	path := map[string]string{"notion": "/pages/", "sentry": "/issues/", "intercom": "/conversations/", "figma": "/files/"}[connection.Provider] + url.PathEscape(id)
	if connection.Provider == "sentry" {
		path += "/"
	}
	if connection.Provider == "figma" {
		path += "?depth=1"
	}
	data, err := s.providerJSON(r.Context(), connection, workspaceKey(r), "GET", path, nil, "")
	if err != nil {
		return providerResult{}, err
	}
	result := providerResult{ID: id}
	switch connection.Provider {
	case "notion":
		result.Title = "Notion page"
		result.URL, _ = data["url"].(string)
		props, _ := data["properties"].(map[string]any)
		for _, v := range props {
			p, _ := v.(map[string]any)
			if p["type"] == "title" {
				result.Title = richText(p["title"])
			}
		}
		blocks, e := s.providerJSON(r.Context(), connection, workspaceKey(r), "GET", "/blocks/"+url.PathEscape(id)+"/children?page_size=100", nil, "")
		if e != nil {
			return result, e
		}
		for _, v := range asArray(blocks["results"]) {
			b, _ := v.(map[string]any)
			kind, _ := b["type"].(string)
			content, _ := b[kind].(map[string]any)
			result.Body += richText(content["rich_text"]) + "\n"
		}
	case "sentry":
		result.Title, _ = data["title"].(string)
		result.URL, _ = data["permalink"].(string)
		result.Body = fmt.Sprintf("%s\n\nOccurrences: %v\nFirst seen: %v\nLast seen: %v", result.Title, data["count"], data["firstSeen"], data["lastSeen"])
	case "intercom":
		result.Title, _ = data["title"].(string)
		source, _ := data["source"].(map[string]any)
		author, _ := source["author"].(map[string]any)
		result.ContactEmail, _ = author["email"].(string)
		result.Body, _ = source["body"].(string)
		if result.Title == "" {
			result.Title = "Conversation " + id
		}
		result.URL = "https://app.intercom.com/a/inbox/" + url.PathEscape(id)
	case "figma":
		result.Title, _ = data["name"].(string)
		result.URL = "https://www.figma.com/file/" + url.PathEscape(id)
		result.Body = fmt.Sprintf("%s\nLast modified: %v", result.Title, data["lastModified"])
	}
	result.Title = truncateSettingsText(result.Title, 500)
	result.Body = truncateSettingsText(result.Body, 32000)
	if !strings.HasPrefix(result.URL, "https://") {
		result.URL = ""
	}
	return result, nil
}
func asArray(v any) []any { items, _ := v.([]any); return items }
func richText(v any) string {
	var b strings.Builder
	for _, v := range asArray(v) {
		item, _ := v.(map[string]any)
		text, _ := item["plain_text"].(string)
		if text == "" {
			value, _ := item["text"].(map[string]any)
			text, _ = value["content"].(string)
		}
		b.WriteString(text)
	}
	return b.String()
}

func (s *server) attachProviderResource(r *http.Request, data domain.Bootstrap, result providerResult, issueID string) error {
	issue, err := s.store.IssueRecord(r.Context(), workspaceKey(r), issueID)
	if err != nil {
		return errNotFound
	}
	if _, err := s.agentIssueContext(r, []string{issue.ID}); err != nil {
		return err
	}
	if slices.ContainsFunc(issue.Attachments, func(a domain.Attachment) bool { return a.URL == result.URL }) {
		return nil
	}
	_, err = invokeJSONHandler(store.WithIssueRecordMutations(r.Context(), issue.ID), "POST", map[string]string{"id": issue.ID}, domain.IssueLinkInput{Title: result.Title, URL: result.URL}, s.createIssueLink)
	return err
}

func (s *server) importProviderResource(r *http.Request, data domain.Bootstrap, connection domain.IntegrationConnection, result providerResult, teamID string) (string, error) {
	if teamID == "" {
		teamID = data.WorkspaceSettings.FeatureSettings.CustomerDefaultTeamID
	}
	if teamID == "" || !slices.ContainsFunc(data.Teams, func(t domain.Team) bool { return t.ID == teamID }) {
		return "", fmt.Errorf("Choose a team for the imported issue")
	}
	id := fmt.Sprintf("external_%x", sha256.Sum256([]byte(connection.ID+"\x00"+result.ID)))
	input := domain.IssueCreateInput{Title: result.Title, Description: result.Body, TeamID: teamID}
	ctx := store.WithIssueCreationKey(r.Context(), id)
	saved, err := invokeJSONHandler(ctx, "POST", nil, input, s.createIssueRecord)
	if err != nil {
		return "", err
	}
	var issue domain.Issue
	if jsonClone(saved, &issue) != nil {
		return "", errInvalid
	}
	if result.URL != "" {
		if err = s.attachProviderResource(r, data, result, issue.ID); err != nil {
			return "", err
		}
	}
	if connection.Provider == "intercom" {
		err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.ask_imported", issue.ID, nil, func(next *domain.Bootstrap) error {
			if slices.ContainsFunc(next.Asks, func(a domain.Ask) bool { return a.ID == id }) {
				return nil
			}
			now := time.Now().UTC()
			next.Asks = append(next.Asks, domain.Ask{ID: id, Title: result.Title, Body: result.Body, Source: "intercom", TeamID: teamID, IssueID: issue.ID, Requester: domain.User{ID: "external", DisplayName: "Intercom requester"}, Status: "approved", Approvals: []domain.AskApproval{}, CreatedAt: now, UpdatedAt: now})
			if workspaceFeatureEnabled(next.WorkspaceSettings, "customer-requests") && !next.WorkspaceSettings.ReduceSupportPersonalInfo && !next.WorkspaceSettings.HIPAACompliance {
				if contact, err := mail.ParseAddress(result.ContactEmail); err == nil && !customerDomainMatches(contact.Address, next.WorkspaceSettings.FeatureSettings.CustomerExcludedDomains) {
					parts := strings.SplitN(strings.ToLower(contact.Address), "@", 2)
					if len(parts) == 2 && !customerDomainMatches(parts[1], next.WorkspaceSettings.FeatureSettings.CustomerGenericDomains) {
						customerID := ""
						for _, customer := range next.Customers {
							if customerDomainMatches(parts[1], customer.Domains) {
								customerID = customer.ID
								break
							}
						}
						if customerID == "" {
							customerID = fmt.Sprintf("customer_%x", sha256.Sum256([]byte(connection.ID+"\x00"+parts[1])))
							next.Customers = append(next.Customers, domain.Customer{ID: customerID, Name: parts[1], Status: "active", Domains: []string{parts[1]}, CreatedAt: now, UpdatedAt: now})
						}
						next.CustomerRequests = append(next.CustomerRequests, domain.CustomerRequest{ID: id + "_request", CustomerID: customerID, IssueID: issue.ID, Body: result.Body, Source: "intercom", SourceURL: result.URL, Creator: data.Viewer, Attachments: []domain.Attachment{}, CreatedAt: now, UpdatedAt: now})
					}
				}
			}
			return nil
		})
	}
	return issue.ID, err
}
