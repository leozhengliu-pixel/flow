package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
)

// Customer identity for integrations (the reference app's managing-customers guide):
// external ids are unique per workspace, and PUT /api/customers (upsert)
// matches an existing customer by id, external id or domain before creating
// one. Matching merges domains and external ids.

const maxCustomerExternalIDs = 50

// validateCustomerIdentity rejects external ids already used by another
// customer and oversized identity lists.
func validateCustomerIdentity(data *domain.Bootstrap, customer domain.Customer) error {
	if len(customer.ExternalIDs) > maxCustomerExternalIDs || len(customer.Domains) > maxCustomerExternalIDs {
		return fmt.Errorf("%w: a customer has at most %d domains and %d external ids", errInvalid, maxCustomerExternalIDs, maxCustomerExternalIDs)
	}
	for _, value := range customer.ExternalIDs {
		if len(value) > 255 {
			return fmt.Errorf("%w: external ids are at most 255 characters", errInvalid)
		}
		for _, other := range data.Customers {
			if other.ID != customer.ID && slices.Contains(other.ExternalIDs, value) {
				return fmt.Errorf("%w: external id %q already identifies customer %s", errInvalid, value, other.ID)
			}
		}
	}
	return nil
}

func customerByExternalID(data *domain.Bootstrap, externalID string) int {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return -1
	}
	return slices.IndexFunc(data.Customers, func(item domain.Customer) bool { return slices.Contains(item.ExternalIDs, externalID) })
}

func customerByDomain(data *domain.Bootstrap, domains []string) int {
	for _, value := range domains {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if index := slices.IndexFunc(data.Customers, func(item domain.Customer) bool {
			return slices.ContainsFunc(item.Domains, func(existing string) bool { return strings.EqualFold(existing, value) })
		}); index >= 0 {
			return index
		}
	}
	return -1
}

type customerUpsertInput = publicapi.CustomerUpsertInput

func mergeStrings(current, added []string) []string {
	result := slices.Clone(current)
	for _, value := range added {
		if !slices.ContainsFunc(result, func(existing string) bool { return strings.EqualFold(existing, value) }) {
			result = append(result, value)
		}
	}
	return result
}

// PUT /api/customers (the reference app customerUpsert)

func (s *server) upsertCustomer(w http.ResponseWriter, r *http.Request) {
	if !s.allowManualCustomerEdit(w, r) {
		return
	}
	var input customerUpsertInput
	if !decodeJSON(w, r, &input) {
		return
	}
	externalIDs := []string{}
	if input.ExternalIDs != nil {
		externalIDs = normalizedStrings(*input.ExternalIDs)
	}
	if value := strings.TrimSpace(input.ExternalID); value != "" {
		externalIDs = mergeStrings(externalIDs, []string{value})
	}
	domains := []string{}
	if input.Domains != nil {
		domains = normalizedStrings(*input.Domains)
	}
	if input.ID == "" && len(externalIDs) == 0 && len(domains) == 0 {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Provide id, externalId(s) or domains to match a customer")
		return
	}
	if err := checkCustomerInput(input.CustomerInput); err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	var saved domain.Customer
	created := false
	// The event type is decided inside the write; the snapshot is checked
	// first so webhooks receive create vs update.
	eventType := "customer.updated"
	if data, ok := s.store.WorkspaceMetadata(workspaceKey(r)); ok && upsertCustomerMatch(&data, input.ID, externalIDs, domains) < 0 {
		eventType = "customer.created"
	}
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), eventType, input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		index := upsertCustomerMatch(data, input.ID, externalIDs, domains)
		if index < 0 && input.ID != "" {
			return "", errNotFound
		}
		if index < 0 {
			if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
				return "", fmt.Errorf("%w: name is required to create a customer", errInvalid)
			}
			data.Customers = append(data.Customers, domain.Customer{ID: fmt.Sprintf("customer_%d", now.UnixNano()), Status: defaultCustomerStatus(data), Domains: []string{}, CreatedAt: now, UpdatedAt: now})
			index = len(data.Customers) - 1
			created = true
		}
		customer := &data.Customers[index]
		update := input.CustomerInput
		update.Domains, update.ExternalIDs = nil, nil
		applyCustomerInput(customer, update)
		customer.Domains = mergeStrings(customer.Domains, domains)
		customer.ExternalIDs = mergeStrings(customer.ExternalIDs, externalIDs)
		if input.Status != nil {
			status, ok := resolveCustomerStatus(data, *input.Status)
			if !ok {
				return "", fmt.Errorf("%w: unknown customer status", errInvalid)
			}
			customer.Status = status
		}
		for _, value := range customer.Domains {
			if customerDomainMatches(value, data.WorkspaceSettings.FeatureSettings.CustomerGenericDomains) {
				return "", fmt.Errorf("%w: generic domains cannot identify a customer", errInvalid)
			}
		}
		if err := validateCustomerIdentity(data, *customer); err != nil {
			return "", err
		}
		if strings.TrimSpace(customer.Name) == "" {
			return "", fmt.Errorf("%w: name is required", errInvalid)
		}
		customer.UpdatedAt = now
		saved = *customer
		return customer.ID, nil
	})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	respondMutation(w, err, status, saved)
}

func upsertCustomerMatch(data *domain.Bootstrap, id string, externalIDs, domains []string) int {
	if id != "" {
		return slices.IndexFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id })
	}
	for _, externalID := range externalIDs {
		if index := customerByExternalID(data, externalID); index >= 0 {
			return index
		}
	}
	return customerByDomain(data, domains)
}

// customerRequestAPIInputs validates the integration-only request fields.
func (s *server) customerRequestAPIInputs(w http.ResponseWriter, input customerRequestInput) (string, string, bool) {
	externalID, attachmentURL := trimmedValue(input.CustomerExternalID), trimmedValue(input.AttachmentURL)
	if attachmentURL != "" {
		normalized, ok := validHTTPURL(attachmentURL, 2048)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "attachmentUrl must be an http or https URL")
			return "", "", false
		}
		attachmentURL = normalized
	}
	if externalID != "" && trimmedValue(input.CustomerID) != "" {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Provide customerId or customerExternalId, not both")
		return "", "", false
	}
	return externalID, attachmentURL, true
}

// attachCustomerRequestLink adds the request's attachmentUrl to its issue as
// a link attachment (idempotent by URL). Failures leave the request intact.
func (s *server) attachCustomerRequestLink(r *http.Request, request domain.CustomerRequest, link string) {
	_, query, err := s.publicIssueQuery(r)
	if err != nil {
		return
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, request.IssueID)
	if err != nil {
		return
	}
	title := "Customer request"
	_, _, _ = s.saveLinkAttachment(r.Context(), r, workspaceKey(r), issue, linkAttachmentInput{URL: link, Title: &title})
}
