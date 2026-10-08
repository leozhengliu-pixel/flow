package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type customerMergeInput struct {
	TargetCustomerID string `json:"targetCustomerId"`
}

// mergeCustomer merges the customer in the path into targetCustomerId, like
// the reference app's "Merge with…": the target keeps its name, status, tier,
// owner, revenue and size; the source's requests move to the target, project
// links follow, domains are combined, and the source is deleted.
func (s *server) mergeCustomer(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")
	var input customerMergeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	targetID := strings.TrimSpace(input.TargetCustomerID)
	if targetID == "" || targetID == sourceID {
		writeError(w, http.StatusBadRequest, "targetCustomerId must name another customer")
		return
	}
	var merged domain.Customer
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer.merged", sourceID, input, func(data *domain.Bootstrap) error {
		sourceIndex := slices.IndexFunc(data.Customers, func(customer domain.Customer) bool { return customer.ID == sourceID })
		targetIndex := slices.IndexFunc(data.Customers, func(customer domain.Customer) bool { return customer.ID == targetID })
		if sourceIndex < 0 || targetIndex < 0 {
			return errNotFound
		}
		source := data.Customers[sourceIndex]
		target := &data.Customers[targetIndex]
		now := time.Now().UTC()
		domains := normalizedStrings(append(slices.Clone(target.Domains), source.Domains...))
		slices.Sort(domains)
		target.Domains = domains
		if target.OwnerID == "" {
			target.OwnerID = source.OwnerID
		}
		if target.LogoURL == "" {
			target.LogoURL = source.LogoURL
		}
		target.UpdatedAt = now
		merged = *target
		moved := 0
		for index := range data.CustomerRequests {
			if data.CustomerRequests[index].CustomerID == sourceID {
				data.CustomerRequests[index].CustomerID = targetID
				data.CustomerRequests[index].UpdatedAt = now
				moved++
			}
		}
		for index := range data.Projects {
			customers := data.Projects[index].Customers
			if !slices.Contains(customers, sourceID) {
				continue
			}
			next := []string{}
			for _, value := range customers {
				if value == sourceID {
					value = targetID
				}
				if !slices.Contains(next, value) {
					next = append(next, value)
				}
			}
			data.Projects[index].Customers = next
		}
		removeResourcePreferences(data, "customer", sourceID)
		data.Customers = slices.Delete(data.Customers, sourceIndex, sourceIndex+1)
		appendAudit(data, "merged", "customer", targetID, map[string]any{"sourceCustomerId": sourceID, "movedRequests": moved})
		return nil
	})
	respondMutation(w, err, http.StatusOK, merged)
}
