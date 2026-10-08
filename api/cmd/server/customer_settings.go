package main

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func customerDomainMatches(value string, entries []string) bool {
	return customerDomainMatch(value, entries) != ""
}

// customerDomainMatch returns the first entry (as stored) that matches value,
// an email address or a domain. Domain entries also match their subdomains.
func customerDomainMatch(value string, entries []string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, raw := range entries {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if value == entry {
			return raw
		}
		if !strings.Contains(entry, "@") {
			domain := strings.TrimLeft(entry, "@.")
			candidate := value
			if at := strings.LastIndex(candidate, "@"); at >= 0 {
				candidate = candidate[at+1:]
			}
			if candidate == domain || strings.HasSuffix(candidate, "."+domain) {
				return raw
			}
		}
	}
	return ""
}

func (s *server) allowManualCustomerEdit(w http.ResponseWriter, r *http.Request) bool {
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		return true
	}
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if !data.WorkspaceSettings.FeatureSettings.CustomerManualEdits {
		writeError(w, http.StatusForbidden, "manual customer attribute edits are disabled")
		return false
	}
	return true
}

// Customer revenue display options (Settings › Customer requests › Display options).
var customerRevenueFormats = []string{"annual", "monthly"}

// customerRevenueCurrencies are the currencies offered for customer revenue.
var customerRevenueCurrencies = []string{"AUD", "BRL", "CAD", "CHF", "CNY", "DKK", "EUR", "GBP", "HKD", "INR", "JPY", "KRW", "MXN", "NOK", "NZD", "SEK", "SGD", "TWD", "USD", "ZAR"}

// applyCustomerExclusionChanges keeps existing customer requests consistent
// with the excluded domains and emails list. Requests whose sender matches a
// newly excluded entry are archived; requests archived by an entry that was
// removed are restored (or re-attributed to another entry that still matches).
// Only workspace metadata (customer requests) is touched, never issues.
func applyCustomerExclusionChanges(data *domain.Bootstrap, before, after []string, now time.Time) {
	added := slices.DeleteFunc(slices.Clone(after), func(entry string) bool { return customerSourceListed(before, entry) })
	removed := slices.DeleteFunc(slices.Clone(before), func(entry string) bool { return customerSourceListed(after, entry) })
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	for index := range data.CustomerRequests {
		request := &data.CustomerRequests[index]
		sender := strings.TrimSpace(request.Creator.Email)
		if request.ArchivedAt == nil {
			if sender == "" || len(added) == 0 {
				continue
			}
			if entry := customerDomainMatch(sender, added); entry != "" {
				archivedAt := now
				request.ArchivedAt = &archivedAt
				request.ArchivedByExclusion = entry
				request.UpdatedAt = now
			}
			continue
		}
		if request.ArchivedByExclusion == "" || !customerSourceListed(removed, request.ArchivedByExclusion) {
			continue
		}
		if entry := customerDomainMatch(sender, after); sender != "" && entry != "" {
			request.ArchivedByExclusion = entry
			continue
		}
		request.ArchivedAt = nil
		request.ArchivedByExclusion = ""
		request.UpdatedAt = now
	}
}

func customerSourceListed(values []string, value string) bool {
	return slices.ContainsFunc(values, func(item string) bool { return strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) })
}

const (
	customerTaxonomyMaxNameLength        = 25
	customerTaxonomyMaxDescriptionLength = 255
)

var customerTaxonomyColorPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// customerTaxonomy is either the workspace's customer statuses or its tiers.
// Both share one shape, so handlers work on []domain.CustomerStatus and
// convert tiers on the way in and out.
type customerTaxonomy struct{ kind string }

func customerTaxonomyKind(r *http.Request) customerTaxonomy {
	if strings.Contains(r.URL.Path, "customer-tiers") {
		return customerTaxonomy{kind: "tier"}
	}
	return customerTaxonomy{kind: "status"}
}

func (t customerTaxonomy) label() string {
	if t.kind == "tier" {
		return "customer tier"
	}
	return "customer status"
}

func (t customerTaxonomy) defaultColor() string {
	if t.kind == "tier" {
		return "#8a8f98"
	}
	return "#95a2b3"
}

func (t customerTaxonomy) items(data *domain.Bootstrap) []domain.CustomerStatus {
	if t.kind == "status" {
		return data.CustomerStatuses
	}
	items := make([]domain.CustomerStatus, len(data.CustomerTiers))
	for index, tier := range data.CustomerTiers {
		items[index] = domain.CustomerStatus(tier)
	}
	return items
}

func (t customerTaxonomy) store(data *domain.Bootstrap, items []domain.CustomerStatus) {
	if t.kind == "status" {
		data.CustomerStatuses = items
		return
	}
	tiers := make([]domain.CustomerTier, len(items))
	for index, item := range items {
		tiers[index] = domain.CustomerTier(item)
	}
	data.CustomerTiers = tiers
}

// customerValue is the customer field that stores the status or tier. It
// holds the item's name (older data may hold its id).
func (t customerTaxonomy) customerValue(customer *domain.Customer) *string {
	if t.kind == "tier" {
		return &customer.Tier
	}
	return &customer.Status
}

func customerTaxonomyRefersTo(value string, item domain.CustomerStatus) bool {
	value = strings.TrimSpace(value)
	return value != "" && (value == item.ID || strings.EqualFold(value, item.Name))
}

func (t customerTaxonomy) validate(items []domain.CustomerStatus, selfID, name, description, color string) error {
	if name == "" {
		return fmt.Errorf("%w: The %s name cannot be empty.", errInvalid, t.label())
	}
	if utf8.RuneCountInString(name) > customerTaxonomyMaxNameLength {
		return fmt.Errorf("%w: Name is too long.", errInvalid)
	}
	if utf8.RuneCountInString(description) > customerTaxonomyMaxDescriptionLength {
		return fmt.Errorf("%w: Description is too long.", errInvalid)
	}
	if color != "" && !customerTaxonomyColorPattern.MatchString(color) {
		return fmt.Errorf("%w: Color must be a hex color.", errInvalid)
	}
	if slices.ContainsFunc(items, func(item domain.CustomerStatus) bool {
		return item.ID != selfID && item.ArchivedAt == nil && strings.EqualFold(item.Name, name)
	}) {
		return fmt.Errorf("%w: A %s with this name already exists.", errInvalid, t.label())
	}
	return nil
}

// reassignCustomers moves customers off an archived or deleted item: statuses
// fall back to the first remaining status, tiers are cleared. It returns the
// customers it changed so the change can be undone.
func (t customerTaxonomy) reassignCustomers(data *domain.Bootstrap, items []domain.CustomerStatus, removed domain.CustomerStatus) []string {
	replacement := ""
	if t.kind == "status" {
		remaining := slices.DeleteFunc(slices.Clone(items), func(item domain.CustomerStatus) bool {
			return item.ID == removed.ID || item.ArchivedAt != nil
		})
		slices.SortStableFunc(remaining, func(a, b domain.CustomerStatus) int {
			switch {
			case a.Position < b.Position:
				return -1
			case a.Position > b.Position:
				return 1
			}
			return 0
		})
		if len(remaining) > 0 {
			replacement = remaining[0].Name
		}
	}
	changed := []string{}
	for index := range data.Customers {
		value := t.customerValue(&data.Customers[index])
		if customerTaxonomyRefersTo(*value, removed) {
			*value = replacement
			changed = append(changed, data.Customers[index].ID)
		}
	}
	return changed
}

type customerTaxonomyInput struct {
	Name               *string  `json:"name"`
	Description        *string  `json:"description"`
	Color              *string  `json:"color"`
	Position           *float64 `json:"position"`
	Archived           *bool    `json:"archived"`
	RestoreCustomerIDs []string `json:"restoreCustomerIds"`
}

// customerTaxonomyResult is a status or tier plus the customers a delete
// (archive) reassigned, which the client passes back to undo it.
type customerTaxonomyResult struct {
	domain.CustomerStatus
	ReassignedCustomerIDs []string `json:"reassignedCustomerIds,omitempty"`
}

func requireCustomerTaxonomyEditor(data *domain.Bootstrap) error {
	if data.ViewerRole == "guest" {
		return store.ErrAuthForbidden
	}
	return nil
}

func (s *server) listCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	writeJSON(w, http.StatusOK, map[string]any{"statuses": data.CustomerStatuses, "tiers": data.CustomerTiers})
}

func (s *server) createCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	var input customerTaxonomyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	taxonomy := customerTaxonomyKind(r)
	var result domain.CustomerStatus
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_taxonomy.created", taxonomy.kind, input, func(data *domain.Bootstrap) error {
		if err := requireCustomerTaxonomyEditor(data); err != nil {
			return err
		}
		items := taxonomy.items(data)
		name, description, color := "", "", taxonomy.defaultColor()
		if input.Name != nil {
			name = strings.TrimSpace(*input.Name)
		}
		if input.Description != nil {
			description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil && strings.TrimSpace(*input.Color) != "" {
			color = strings.ToLower(strings.TrimSpace(*input.Color))
		}
		if err := taxonomy.validate(items, "", name, description, color); err != nil {
			return err
		}
		// A deleted item with the same name would make name lookups ambiguous.
		items = slices.DeleteFunc(items, func(item domain.CustomerStatus) bool {
			return item.ArchivedAt != nil && strings.EqualFold(item.Name, name)
		})
		position := 0.0
		for index, item := range items {
			if index == 0 || item.Position+1 > position {
				position = item.Position + 1
			}
		}
		if input.Position != nil {
			position = *input.Position
		}
		now := time.Now().UTC()
		result = domain.CustomerStatus{ID: parityID("customer_" + taxonomy.kind), Name: name, Description: description, Color: color, Position: position, CreatedAt: now, UpdatedAt: now}
		taxonomy.store(data, append(items, result))
		return nil
	})
	respondMutation(w, err, http.StatusCreated, result)
}

func (s *server) updateCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	var input customerTaxonomyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	taxonomy := customerTaxonomyKind(r)
	id := r.PathValue("id")
	var result customerTaxonomyResult
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_taxonomy.updated", id, input, func(data *domain.Bootstrap) error {
		if err := requireCustomerTaxonomyEditor(data); err != nil {
			return err
		}
		items := slices.Clone(taxonomy.items(data))
		index := slices.IndexFunc(items, func(item domain.CustomerStatus) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := items[index]
		previous := item
		if input.Name != nil {
			item.Name = strings.TrimSpace(*input.Name)
		}
		if input.Description != nil {
			item.Description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil && strings.TrimSpace(*input.Color) != "" {
			item.Color = strings.ToLower(strings.TrimSpace(*input.Color))
		}
		if input.Position != nil {
			item.Position = *input.Position
		}
		if input.Name != nil || input.Description != nil || input.Color != nil {
			if err := taxonomy.validate(items, item.ID, item.Name, item.Description, item.Color); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		// Customers store the item's name, so a rename follows it.
		if item.Name != previous.Name {
			for customerIndex := range data.Customers {
				value := taxonomy.customerValue(&data.Customers[customerIndex])
				if *value != previous.ID && strings.EqualFold(strings.TrimSpace(*value), previous.Name) {
					*value = item.Name
				}
			}
		}
		var reassigned []string
		if input.Archived != nil {
			if *input.Archived && item.ArchivedAt == nil {
				item.ArchivedAt = &now
				reassigned = taxonomy.reassignCustomers(data, items, item)
			} else if !*input.Archived && item.ArchivedAt != nil {
				if slices.ContainsFunc(items, func(other domain.CustomerStatus) bool {
					return other.ID != item.ID && other.ArchivedAt == nil && strings.EqualFold(other.Name, item.Name)
				}) {
					return fmt.Errorf("%w: A %s with this name already exists.", errInvalid, taxonomy.label())
				}
				item.ArchivedAt = nil
				for customerIndex := range data.Customers {
					if slices.Contains(input.RestoreCustomerIDs, data.Customers[customerIndex].ID) {
						*taxonomy.customerValue(&data.Customers[customerIndex]) = item.Name
					}
				}
			}
		}
		item.UpdatedAt = now
		items[index] = item
		taxonomy.store(data, items)
		result = customerTaxonomyResult{CustomerStatus: item, ReassignedCustomerIDs: reassigned}
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) deleteCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	taxonomy := customerTaxonomyKind(r)
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_taxonomy.deleted", id, nil, func(data *domain.Bootstrap) error {
		if err := requireCustomerTaxonomyEditor(data); err != nil {
			return err
		}
		items := slices.Clone(taxonomy.items(data))
		index := slices.IndexFunc(items, func(item domain.CustomerStatus) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		removed := items[index]
		if removed.ArchivedAt == nil {
			taxonomy.reassignCustomers(data, items, removed)
		}
		taxonomy.store(data, slices.Delete(items, index, index+1))
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizedCustomerSources trims, lowercases and de-duplicates excluded or
// generic domain/email entries (at most 100 characters each).
func normalizedCustomerSources(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || utf8.RuneCountInString(value) > 100 || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	return result
}
