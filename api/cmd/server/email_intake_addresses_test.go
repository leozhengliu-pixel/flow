package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type intakeCreateResponse struct {
	Address      emailIntakeAddressView `json:"address"`
	InboundToken string                 `json:"inboundToken"`
}

func TestSystemEmailIntakeAddressWizardFlow(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := repository.Bootstrap()
	teamID := bootstrap.Teams[0].ID
	base := "/api/teams/" + teamID + "/email-intake-addresses"

	// Step 1 (team + template) creates an address on Flow's intake domain that
	// receives mail right away.
	created := requestJSON[intakeCreateResponse](t, handler, http.MethodPost, base, map[string]any{"type": "asks"}, http.StatusCreated)
	address := created.Address
	if !address.System || address.Type != "asks" || address.VerificationState != "verified" || !strings.HasSuffix(address.Address, "@"+emailIntakeDomain()) || !address.CustomerRequestsEnabled {
		t.Fatalf("system intake address was not created ready to receive: %#v", address)
	}
	if address.InboundTokenHash != "" || len(address.DNSRecords) != 0 || address.OutboundFromEmail == "" {
		t.Fatalf("public address leaked secrets or invented DNS records: %#v", address)
	}

	// Step 2 stores the sender name and the forwarding (custom) address.
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+address.ID, map[string]any{"senderName": " "}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+address.ID, map[string]any{"forwardingEmailAddress": "not-an-email"}, http.StatusBadRequest)
	updated := requestJSON[emailIntakeAddressView](t, handler, http.MethodPatch, base+"/"+address.ID, map[string]any{"senderName": "Helpdesk", "forwardingEmailAddress": "Helpdesk@Acme.test"}, http.StatusOK)
	if updated.SenderName != "Helpdesk" || updated.ForwardingEmailAddress != "helpdesk@acme.test" || len(updated.DNSRecords) != 1 || updated.DNSRecords[0].Name != "_flow-intake.acme.test" || updated.DNSRecords[0].IsVerified {
		t.Fatalf("forwarding address did not produce DNS records: %#v", updated)
	}
	second := requestJSON[intakeCreateResponse](t, handler, http.MethodPost, base, map[string]any{"type": "asks"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+second.Address.ID, map[string]any{"forwardingEmailAddress": "helpdesk@acme.test"}, http.StatusConflict)

	// Step 3 verifies the custom sending domain.
	verified := requestJSON[emailIntakeAddressView](t, handler, http.MethodPost, base+"/"+address.ID+"/verify", map[string]string{"txtValue": updated.DNSRecords[0].Content}, http.StatusOK)
	if verified.ForwardingDomainVerified == nil || !verified.DNSRecords[0].IsVerified || verified.VerificationState != "verified" {
		t.Fatalf("forwarding domain was not verified: %#v", verified)
	}

	// Mail creates an Ask, and links a customer request only for a matching,
	// non-generic, non-excluded sender domain.
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Acme", "domains": []string{"acme.test"}}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", map[string]any{"messageId": "m-1", "from": "Ada <ada@acme.test>", "subject": "Export is broken", "text": "Steps"}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", map[string]any{"messageId": "m-2", "from": "someone@gmail.com", "subject": "Generic sender", "text": "Body"}, http.StatusCreated)
	after := repository.Bootstrap()
	requests := slices.DeleteFunc(slices.Clone(after.CustomerRequests), func(item domain.CustomerRequest) bool { return item.Source != "email" })
	if len(requests) != 1 || requests[0].CustomerID != customer.ID || requests[0].IssueID != issue.ID {
		t.Fatalf("email customer requests = %#v", requests)
	}
	if !slices.ContainsFunc(after.Asks, func(item domain.Ask) bool { return item.IssueID == issue.ID && item.Source == "email" }) {
		t.Fatal("asks intake address did not create an Ask")
	}

	// Turning the toggle off stops linking.
	requestJSON[emailIntakeAddressView](t, handler, http.MethodPatch, base+"/"+address.ID, map[string]any{"customerRequestsEnabled": false}, http.StatusOK)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", map[string]any{"messageId": "m-3", "from": "bob@acme.test", "subject": "Another", "text": "Body"}, http.StatusCreated)
	if count := len(slices.DeleteFunc(slices.Clone(repository.Bootstrap().CustomerRequests), func(item domain.CustomerRequest) bool { return item.Source != "email" })); count != 1 {
		t.Fatalf("disabled toggle still linked customer requests: %d", count)
	}

	// Reset generates a brand-new address and keeps the old one as an alias.
	type rotateResponse struct {
		Address emailIntakeAddressView `json:"address"`
	}
	rotated := requestJSON[rotateResponse](t, handler, http.MethodPost, base+"/"+address.ID+"/rotate", nil, http.StatusOK)
	if rotated.Address.Address == address.Address || len(rotated.Address.Aliases) != 1 || rotated.Address.Aliases[0].TokenHash != "" {
		t.Fatalf("system address reset failed: %#v", rotated.Address)
	}
}

func TestEmailIntakeExcludedDomainAndTemplate(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := repository.Bootstrap()
	teamID := bootstrap.Teams[0].ID
	base := "/api/teams/" + teamID + "/email-intake-addresses"
	template := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/teams/"+teamID+"/templates", map[string]any{"name": "Bug", "title": "Template title", "priority": 2}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"type": "asks", "templateId": "missing"}, http.StatusBadRequest)
	created := requestJSON[intakeCreateResponse](t, handler, http.MethodPost, base, map[string]any{"type": "asks", "templateId": template.ID, "customerRequestsEnabled": true}, http.StatusCreated)
	requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Globex", "domains": []string{"globex.test"}}, http.StatusCreated)
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"customerExcludedDomains": []string{"globex.test"}}}, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+created.InboundToken+"/receive", map[string]any{"messageId": "x-1", "from": "eve@globex.test", "subject": "Email subject wins", "text": "Body"}, http.StatusCreated)
	if issue.Title != "Email subject wins" || issue.Priority != 2 {
		t.Fatalf("template was not applied under the email's title: %#v", issue)
	}
	if slices.ContainsFunc(repository.Bootstrap().CustomerRequests, func(item domain.CustomerRequest) bool { return item.IssueID == issue.ID }) {
		t.Fatal("excluded domain created a customer request")
	}
}
