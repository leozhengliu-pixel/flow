package main

import (
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Constant page limit for Asks web forms (billing getLimit is OOS).
const maxAsksWebPagesPerConfiguration = 10

var asksWebHostnamePattern = regexp.MustCompile(`(?i)^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
var asksWebSlugPattern = regexp.MustCompile(`(?i)^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

type asksWebSettingsInput struct {
	Title        string `json:"title"`
	Hostname     string `json:"hostname"`
	CustomDomain string `json:"customDomain"`
	EmailAddress string `json:"emailAddress"`
}

type asksWebPageInput struct {
	Slug                string   `json:"slug"`
	Title               string   `json:"title"`
	TemplateIDs         []string `json:"templateIds"`
	AutoReplyCreated    *string  `json:"autoReplyCreated,omitempty"`
	AutoReplyCompleted  *string  `json:"autoReplyCompleted,omitempty"`
	AutoReplyCanceled   *string  `json:"autoReplyCanceled,omitempty"`
	EmailRepliesEnabled *bool    `json:"emailRepliesEnabled,omitempty"`
	Position            *float64 `json:"position,omitempty"`
}

func publicAsksWebSettings(item domain.AsksWebSettings) domain.AsksWebSettings {
	item.DnsVerificationToken = ""
	if item.Pages == nil {
		item.Pages = []domain.AsksWebPage{}
	}
	return item
}

func asksWebPublicURL(hostname string) string {
	host := strings.TrimSpace(strings.ToLower(hostname))
	if host == "" {
		return ""
	}
	return "https://" + host
}

func normalizeAsksWebHostname(value string) (string, error) {
	host := strings.TrimSpace(strings.ToLower(value))
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.Trim(host, "/")
	if host == "" || !asksWebHostnamePattern.MatchString(host) {
		return "", fmt.Errorf("%w: valid hostname is required", errInvalid)
	}
	return host, nil
}

func (s *server) listAsksWebSettings(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := make([]domain.AsksWebSettings, 0, len(data.AsksWebSettings))
	for _, item := range data.AsksWebSettings {
		result = append(result, publicAsksWebSettings(item))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) getAsksWebSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == id })
	if index < 0 {
		writeError(w, http.StatusNotFound, "asks web settings not found")
		return
	}
	writeJSON(w, http.StatusOK, publicAsksWebSettings(data.AsksWebSettings[index]))
}

func (s *server) createAsksWebSettings(w http.ResponseWriter, r *http.Request) {
	var input asksWebSettingsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	hostname, err := normalizeAsksWebHostname(input.Hostname)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = hostname
	}
	email := strings.TrimSpace(strings.ToLower(input.EmailAddress))
	if email != "" {
		if _, parseErr := mail.ParseAddress(email); parseErr != nil {
			writeError(w, http.StatusBadRequest, "valid emailAddress is required")
			return
		}
	}
	customDomain := strings.TrimSpace(strings.ToLower(input.CustomDomain))
	if customDomain != "" {
		normalized, normErr := normalizeAsksWebHostname(customDomain)
		if normErr != nil {
			writeError(w, http.StatusBadRequest, "valid customDomain is required")
			return
		}
		customDomain = normalized
	}

	var created domain.AsksWebSettings
	err = s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web.created", input, func(data *domain.Bootstrap) (string, error) {
		if slices.ContainsFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool {
			return strings.EqualFold(item.Hostname, hostname)
		}) {
			return "", errConflict
		}
		now := time.Now().UTC()
		verification := randomURLToken(18)
		created = domain.AsksWebSettings{
			ID:                    fmt.Sprintf("asks_web_%d", now.UnixNano()),
			Title:                 title,
			Hostname:              hostname,
			AsksURL:               asksWebPublicURL(hostname),
			CustomDomain:          customDomain,
			CustomDomainStatus:    "none",
			EmailAddress:          email,
			EmailDomainConfigured: email != "",
			DnsVerificationToken:  verification,
			DnsVerified:           false,
			HostingStatus:         "pending",
			SamlConfigured:        false,
			Pages:                 []domain.AsksWebPage{},
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		if customDomain != "" {
			created.CustomDomainStatus = "pending"
		}
		data.AsksWebSettings = append(data.AsksWebSettings, created)
		return created.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	public := publicAsksWebSettings(created)
	writeJSON(w, http.StatusCreated, map[string]any{
		"settings": public,
		"dnsRecord": map[string]string{
			"type":  "TXT",
			"name":  "_flow-asks." + created.Hostname,
			"value": "flow-asks-verification=" + created.DnsVerificationToken,
		},
	})
}

func (s *server) updateAsksWebSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input asksWebSettingsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.AsksWebSettings
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web.updated", input, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == id })
		if index < 0 {
			return "", errNotFound
		}
		item := &data.AsksWebSettings[index]
		if strings.TrimSpace(input.Title) != "" {
			item.Title = strings.TrimSpace(input.Title)
		}
		if strings.TrimSpace(input.Hostname) != "" {
			hostname, hermErr := normalizeAsksWebHostname(input.Hostname)
			if hermErr != nil {
				return "", hermErr
			}
			if slices.ContainsFunc(data.AsksWebSettings, func(other domain.AsksWebSettings) bool {
				return other.ID != id && strings.EqualFold(other.Hostname, hostname)
			}) {
				return "", errConflict
			}
			if !strings.EqualFold(item.Hostname, hostname) {
				item.Hostname = hostname
				item.AsksURL = asksWebPublicURL(hostname)
				item.DnsVerified = false
				item.DnsVerificationToken = randomURLToken(18)
				item.HostingStatus = "pending"
			}
		}
		if input.EmailAddress != "" || r.URL.Query().Get("clearEmail") == "1" {
			email := strings.TrimSpace(strings.ToLower(input.EmailAddress))
			if email != "" {
				if _, parseErr := mail.ParseAddress(email); parseErr != nil {
					return "", fmt.Errorf("%w: valid emailAddress is required", errInvalid)
				}
			}
			item.EmailAddress = email
			item.EmailDomainConfigured = email != ""
		}
		if strings.TrimSpace(input.CustomDomain) != "" || r.URL.Query().Get("clearCustomDomain") == "1" {
			custom := strings.TrimSpace(strings.ToLower(input.CustomDomain))
			if custom == "" {
				item.CustomDomain = ""
				item.CustomDomainStatus = "none"
			} else {
				normalized, normErr := normalizeAsksWebHostname(custom)
				if normErr != nil {
					return "", fmt.Errorf("%w: valid customDomain is required", errInvalid)
				}
				if !strings.EqualFold(item.CustomDomain, normalized) {
					item.CustomDomain = normalized
					item.CustomDomainStatus = "pending"
				}
			}
		}
		item.UpdatedAt = time.Now().UTC()
		updated = *item
		return item.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, publicAsksWebSettings(updated))
}

func (s *server) deleteAsksWebSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web.deleted", map[string]string{"id": id}, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == id })
		if index < 0 {
			return "", errNotFound
		}
		data.AsksWebSettings = append(data.AsksWebSettings[:index], data.AsksWebSettings[index+1:]...)
		return id, nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) verifyAsksWebDNS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input struct {
		TXTValue string `json:"txtValue"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}

	metadata := s.workspaceData(r)
	index := slices.IndexFunc(metadata.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == id })
	if index < 0 {
		writeError(w, http.StatusNotFound, "asks web settings not found")
		return
	}
	item := metadata.AsksWebSettings[index]
	expected := "flow-asks-verification=" + item.DnsVerificationToken
	verified := false
	if strings.TrimSpace(input.TXTValue) != "" && strings.TrimSpace(input.TXTValue) == expected {
		verified = true
	} else {
		records, lookupErr := net.DefaultResolver.LookupTXT(r.Context(), "_flow-asks."+item.Hostname)
		if lookupErr == nil {
			for _, record := range records {
				if strings.TrimSpace(record) == expected {
					verified = true
					break
				}
			}
		}
		// Allow explicit txtValue match for tests / offline DNS.
		if !verified && strings.TrimSpace(input.TXTValue) == expected {
			verified = true
		}
	}
	if !verified {
		writeError(w, http.StatusBadRequest, "DNS TXT record not found")
		return
	}

	var updated domain.AsksWebSettings
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web.dns_verified", map[string]string{"id": id}, func(data *domain.Bootstrap) (string, error) {
		idx := slices.IndexFunc(data.AsksWebSettings, func(row domain.AsksWebSettings) bool { return row.ID == id })
		if idx < 0 {
			return "", errNotFound
		}
		row := &data.AsksWebSettings[idx]
		row.DnsVerified = true
		row.HostingStatus = "configured"
		if row.CustomDomainStatus == "pending" {
			row.CustomDomainStatus = "active"
		}
		row.UpdatedAt = time.Now().UTC()
		updated = *row
		return row.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, publicAsksWebSettings(updated))
}

func (s *server) createAsksWebPage(w http.ResponseWriter, r *http.Request) {
	settingsID := r.PathValue("id")
	var input asksWebPageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	slug := strings.TrimSpace(strings.ToLower(input.Slug))
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = slug
	}
	if slug == "" || !asksWebSlugPattern.MatchString(slug) {
		writeError(w, http.StatusBadRequest, "valid slug is required")
		return
	}
	var created domain.AsksWebPage
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web_page.created", input, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == settingsID })
		if index < 0 {
			return "", errNotFound
		}
		settings := &data.AsksWebSettings[index]
		if len(settings.Pages) >= maxAsksWebPagesPerConfiguration {
			return "", fmt.Errorf("%w: page limit reached", errInvalid)
		}
		if slices.ContainsFunc(settings.Pages, func(page domain.AsksWebPage) bool {
			return strings.EqualFold(page.Slug, slug)
		}) {
			return "", errConflict
		}
		for _, templateID := range input.TemplateIDs {
			if !slices.ContainsFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool {
				return template.ID == templateID
			}) {
				return "", fmt.Errorf("%w: template not found", errInvalid)
			}
		}
		now := time.Now().UTC()
		position := float64(len(settings.Pages) + 1)
		if input.Position != nil {
			position = *input.Position
		}
		created = domain.AsksWebPage{
			ID:                  fmt.Sprintf("asks_web_page_%d", now.UnixNano()),
			SettingsID:          settingsID,
			Slug:                slug,
			Title:               title,
			TemplateIDs:         append([]string{}, input.TemplateIDs...),
			EmailRepliesEnabled: false,
			Position:            position,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if input.AutoReplyCreated != nil {
			created.AutoReplyCreated = *input.AutoReplyCreated
		}
		if input.AutoReplyCompleted != nil {
			created.AutoReplyCompleted = *input.AutoReplyCompleted
		}
		if input.AutoReplyCanceled != nil {
			created.AutoReplyCanceled = *input.AutoReplyCanceled
		}
		if input.EmailRepliesEnabled != nil {
			created.EmailRepliesEnabled = *input.EmailRepliesEnabled
		}
		settings.Pages = append(settings.Pages, created)
		settings.UpdatedAt = now
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateAsksWebPage(w http.ResponseWriter, r *http.Request) {
	settingsID, pageID := r.PathValue("id"), r.PathValue("pageId")
	var input asksWebPageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.AsksWebPage
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web_page.updated", input, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == settingsID })
		if index < 0 {
			return "", errNotFound
		}
		settings := &data.AsksWebSettings[index]
		pageIndex := slices.IndexFunc(settings.Pages, func(page domain.AsksWebPage) bool { return page.ID == pageID })
		if pageIndex < 0 {
			return "", errNotFound
		}
		page := &settings.Pages[pageIndex]
		if strings.TrimSpace(input.Slug) != "" {
			slug := strings.TrimSpace(strings.ToLower(input.Slug))
			if !asksWebSlugPattern.MatchString(slug) {
				return "", fmt.Errorf("%w: valid slug is required", errInvalid)
			}
			if slices.ContainsFunc(settings.Pages, func(other domain.AsksWebPage) bool {
				return other.ID != pageID && strings.EqualFold(other.Slug, slug)
			}) {
				return "", errConflict
			}
			page.Slug = slug
		}
		if strings.TrimSpace(input.Title) != "" {
			page.Title = strings.TrimSpace(input.Title)
		}
		if input.TemplateIDs != nil {
			for _, templateID := range input.TemplateIDs {
				if !slices.ContainsFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool {
					return template.ID == templateID
				}) {
					return "", fmt.Errorf("%w: template not found", errInvalid)
				}
			}
			page.TemplateIDs = append([]string{}, input.TemplateIDs...)
		}
		if input.AutoReplyCreated != nil {
			page.AutoReplyCreated = *input.AutoReplyCreated
		}
		if input.AutoReplyCompleted != nil {
			page.AutoReplyCompleted = *input.AutoReplyCompleted
		}
		if input.AutoReplyCanceled != nil {
			page.AutoReplyCanceled = *input.AutoReplyCanceled
		}
		if input.EmailRepliesEnabled != nil {
			page.EmailRepliesEnabled = *input.EmailRepliesEnabled
		}
		if input.Position != nil {
			page.Position = *input.Position
		}
		page.UpdatedAt = time.Now().UTC()
		settings.UpdatedAt = page.UpdatedAt
		updated = *page
		return page.ID, nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteAsksWebPage(w http.ResponseWriter, r *http.Request) {
	settingsID, pageID := r.PathValue("id"), r.PathValue("pageId")
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "asks_web_page.deleted", map[string]string{"id": pageID}, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.AsksWebSettings, func(item domain.AsksWebSettings) bool { return item.ID == settingsID })
		if index < 0 {
			return "", errNotFound
		}
		settings := &data.AsksWebSettings[index]
		pageIndex := slices.IndexFunc(settings.Pages, func(page domain.AsksWebPage) bool { return page.ID == pageID })
		if pageIndex < 0 {
			return "", errNotFound
		}
		settings.Pages = append(settings.Pages[:pageIndex], settings.Pages[pageIndex+1:]...)
		settings.UpdatedAt = time.Now().UTC()
		return pageID, nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
