package main

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// emailIntakeInput creates an intake address. Without a local part and domain
// the address is generated on Flow's intake domain, like Linear's
// <id>@intake.linear.app addresses, and receives mail immediately.
type emailIntakeInput struct {
	LocalPart, Domain       string
	Enabled                 *bool   `json:"enabled,omitempty"`
	Type                    string  `json:"type,omitempty"`
	TemplateID              string  `json:"templateId,omitempty"`
	SenderName              string  `json:"senderName,omitempty"`
	ForwardingEmailAddress  string  `json:"forwardingEmailAddress,omitempty"`
	CustomerRequestsEnabled *bool   `json:"customerRequestsEnabled,omitempty"`
	TeamID                  *string `json:"teamId,omitempty"`
}

type emailIntakeUpdateInput struct {
	TeamID                  *string `json:"teamId,omitempty"`
	TemplateID              *string `json:"templateId,omitempty"`
	SenderName              *string `json:"senderName,omitempty"`
	ForwardingEmailAddress  *string `json:"forwardingEmailAddress,omitempty"`
	CustomerRequestsEnabled *bool   `json:"customerRequestsEnabled,omitempty"`
	Enabled                 *bool   `json:"enabled,omitempty"`
}

// emailIntakeAddressView is the public shape of an intake address: secrets are
// removed and the DNS records for its custom sending domain are attached.
type emailIntakeAddressView struct {
	domain.EmailIntakeAddress
	DNSRecords        []domain.EmailIntakeDNSRecord `json:"dnsRecords"`
	OutboundFromEmail string                        `json:"outboundFromEmail"`
}

func emailIntakeDomain() string {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_EMAIL_INTAKE_DOMAIN"))); value != "" {
		return value
	}
	return "intake.flow.app"
}

// emailIntakeFromEmail is the default sender for intake replies, the
// equivalent of Linear's issues@linear.app.
func emailIntakeFromEmail() string {
	if value := strings.TrimSpace(os.Getenv("FLOW_SMTP_FROM")); value != "" {
		if parsed, err := mail.ParseAddress(value); err == nil {
			return parsed.Address
		}
	}
	return "issues@" + strings.TrimPrefix(emailIntakeDomain(), "intake.")
}

func emailDomain(address string) string {
	if at := strings.LastIndex(address, "@"); at >= 0 {
		return strings.ToLower(address[at+1:])
	}
	return ""
}

func randomIntakeLocalPart() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buffer := make([]byte, 10)
	_, _ = rand.Read(buffer)
	for index := range buffer {
		buffer[index] = alphabet[int(buffer[index])%len(alphabet)]
	}
	return string(buffer)
}

func publicEmailIntakeAddress(item domain.EmailIntakeAddress) emailIntakeAddressView {
	item.InboundTokenHash = ""
	item.Aliases = slices.Clone(item.Aliases)
	for index := range item.Aliases {
		item.Aliases[index].TokenHash = ""
	}
	view := emailIntakeAddressView{EmailIntakeAddress: item, DNSRecords: []domain.EmailIntakeDNSRecord{}, OutboundFromEmail: emailIntakeFromEmail()}
	if item.System && item.ForwardingEmailAddress != "" && item.VerificationToken != "" {
		view.DNSRecords = append(view.DNSRecords, domain.EmailIntakeDNSRecord{Type: "TXT", Name: "_flow-intake." + emailDomain(item.ForwardingEmailAddress), Content: "flow-verification=" + item.VerificationToken, IsVerified: item.ForwardingDomainVerified != nil})
	}
	return view
}

// validateEmailIntakeTemplate mirrors Linear: only the team's standard
// templates can fill issues created from email.
func validateEmailIntakeTemplate(data *domain.Bootstrap, teamID, templateID string) error {
	if templateID == "" {
		return nil
	}
	index := slices.IndexFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == templateID })
	if index < 0 {
		return errInvalid
	}
	template := data.IssueTemplates[index]
	if template.TemplateType == "form" {
		return fmt.Errorf("%w: form templates are not available for email intake", errInvalid)
	}
	if template.TeamID == "" {
		return fmt.Errorf("%w: workspace templates are not available for email intake", errInvalid)
	}
	return nil
}

func validateForwardingEmailAddress(data *domain.Bootstrap, id, value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return "", fmt.Errorf("%w: please enter a valid email address", errInvalid)
	}
	if slices.ContainsFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool {
		return item.ID != id && item.Enabled && (strings.EqualFold(item.ForwardingEmailAddress, value) || strings.EqualFold(item.Address, value))
	}) {
		return "", fmt.Errorf("%w: email address already in use", errConflict)
	}
	return value, nil
}

func (s *server) listEmailIntakeAddresses(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	result := make([]emailIntakeAddressView, 0)
	for _, item := range data.EmailIntakeAddresses {
		if item.TeamID == teamID {
			result = append(result, publicEmailIntakeAddress(item))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	var input emailIntakeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID := r.PathValue("id")
	input.LocalPart = strings.ToLower(strings.TrimSpace(input.LocalPart))
	input.Domain = strings.ToLower(strings.TrimSpace(input.Domain))
	system := input.LocalPart == "" && input.Domain == ""
	if !system {
		if _, err := mail.ParseAddress(input.LocalPart + "@" + input.Domain); err != nil {
			writeError(w, http.StatusBadRequest, "valid localPart and domain are required")
			return
		}
	}
	if input.Type != "" && input.Type != "asks" && input.Type != "team" {
		writeError(w, http.StatusBadRequest, "type must be asks or team")
		return
	}
	var created domain.EmailIntakeAddress
	var inboundToken string
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "email_intake.created", input, func(data *domain.Bootstrap) (string, error) {
		if !teamExists(data, teamID) {
			return "", errNotFound
		}
		if err := validateEmailIntakeTemplate(data, teamID, input.TemplateID); err != nil {
			return "", err
		}
		forwarding, err := validateForwardingEmailAddress(data, "", input.ForwardingEmailAddress)
		if err != nil {
			return "", err
		}
		now := time.Now().UTC()
		localPart, intakeDomain := input.LocalPart, input.Domain
		if system {
			intakeDomain = emailIntakeDomain()
			for localPart == "" || slices.ContainsFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.Address == localPart+"@"+intakeDomain }) {
				localPart = randomIntakeLocalPart()
			}
		}
		address := localPart + "@" + intakeDomain
		if slices.ContainsFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool {
			return strings.EqualFold(item.Address, address) && item.Enabled
		}) {
			return "", errConflict
		}
		inboundToken = randomURLToken(24)
		created = domain.EmailIntakeAddress{ID: fmt.Sprintf("email_intake_%d", now.UnixNano()), TeamID: teamID, LocalPart: localPart, Domain: intakeDomain, Address: address, InboundTokenHash: secretHash(inboundToken), VerificationToken: randomURLToken(18), VerificationState: "pending", Aliases: []domain.EmailIntakeAlias{}, Enabled: true, Type: input.Type, System: system, TemplateID: input.TemplateID, SenderName: strings.TrimSpace(input.SenderName), ForwardingEmailAddress: forwarding, CreatedAt: now, UpdatedAt: now}
		if system {
			// Flow owns the intake domain, so the address receives immediately.
			created.VerificationState = "verified"
			created.VerifiedAt = &now
		}
		if input.Enabled != nil {
			created.Enabled = *input.Enabled
		}
		if input.CustomerRequestsEnabled != nil {
			created.CustomerRequestsEnabled = *input.CustomerRequestsEnabled
		} else if input.Type == "asks" {
			created.CustomerRequestsEnabled = customerRequestsFeatureEnabled(data)
		}
		data.EmailIntakeAddresses = append(data.EmailIntakeAddresses, created)
		return created.ID, nil
	})
	if err == nil {
		view := publicEmailIntakeAddress(created)
		dnsRecord := map[string]string{"type": "TXT", "name": "_flow-intake." + created.Domain, "value": "flow-verification=" + created.VerificationToken}
		writeJSON(w, http.StatusCreated, map[string]any{"address": view, "inboundToken": inboundToken, "dnsRecord": dnsRecord})
		return
	}
	writeEmailIntakeError(w, err)
}

func writeEmailIntakeError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "already in use"):
		writeError(w, http.StatusConflict, "Email address already in use")
	case strings.Contains(message, ": "):
		writeError(w, http.StatusBadRequest, message[strings.Index(message, ": ")+2:])
	default:
		respondMutation(w, err, http.StatusOK, nil)
	}
}

func (s *server) updateEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	var input emailIntakeUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	var updated domain.EmailIntakeAddress
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.EmailIntakeAddresses[index]
		if input.TeamID != nil && *input.TeamID != item.TeamID {
			if !teamExists(data, *input.TeamID) {
				return errInvalid
			}
			// Linear clears the template whenever the team changes.
			item.TeamID, item.TemplateID = *input.TeamID, ""
		}
		if input.TemplateID != nil {
			if err := validateEmailIntakeTemplate(data, item.TeamID, *input.TemplateID); err != nil {
				return err
			}
			item.TemplateID = *input.TemplateID
		}
		if input.SenderName != nil {
			name := strings.TrimSpace(*input.SenderName)
			if name == "" && item.Type == "asks" {
				return fmt.Errorf("%w: please enter a name", errInvalid)
			}
			item.SenderName = name
		}
		if input.ForwardingEmailAddress != nil {
			forwarding, err := validateForwardingEmailAddress(data, item.ID, *input.ForwardingEmailAddress)
			if err != nil {
				return err
			}
			if emailDomain(forwarding) != emailDomain(item.ForwardingEmailAddress) {
				item.ForwardingDomainVerified = nil
				item.VerificationToken = randomURLToken(18)
			}
			item.ForwardingEmailAddress = forwarding
		}
		if input.CustomerRequestsEnabled != nil {
			item.CustomerRequestsEnabled = *input.CustomerRequestsEnabled
		}
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
		}
		item.UpdatedAt = time.Now().UTC()
		updated = *item
		return nil
	})
	if err != nil {
		writeEmailIntakeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, publicEmailIntakeAddress(updated))
}

func (s *server) verifyEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TXTValue string `json:"txtValue"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	metadata := s.workspaceData(r)
	addressIndex := slices.IndexFunc(metadata.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
	if addressIndex < 0 {
		writeError(w, http.StatusNotFound, "intake address not found")
		return
	}
	address := metadata.EmailIntakeAddresses[addressIndex]
	// System addresses verify the forwarding domain used for outbound replies;
	// legacy addresses verify the domain that receives mail.
	verifyDomain := address.Domain
	if address.System {
		verifyDomain = emailDomain(address.ForwardingEmailAddress)
		if verifyDomain == "" {
			writeError(w, http.StatusBadRequest, "add a custom address before verifying DNS")
			return
		}
	}
	expectedToken := address.VerificationToken
	expectedTXT := "flow-verification=" + expectedToken
	verified := expectedToken != "" && s.authDisabled && strings.TrimSpace(input.TXTValue) == expectedTXT
	if !verified && expectedToken != "" {
		records, err := net.DefaultResolver.LookupTXT(r.Context(), "_flow-intake."+verifyDomain)
		verified = err == nil && slices.Contains(records, expectedTXT)
	}
	if !verified {
		writeError(w, http.StatusBadRequest, "TXT verification record was not found")
		return
	}
	var updated domain.EmailIntakeAddress
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.verified", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.EmailIntakeAddresses[index]
		if item.VerificationToken != expectedToken {
			return errConflict
		}
		now := time.Now().UTC()
		if item.System {
			item.ForwardingDomainVerified = &now
		} else {
			item.VerificationState = "verified"
			item.VerifiedAt = &now
			item.VerificationToken = ""
		}
		item.UpdatedAt = now
		updated = *item
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, publicEmailIntakeAddress(updated))
}

func (s *server) rotateEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	var result map[string]any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.rotated", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		item := &data.EmailIntakeAddresses[index]
		now := time.Now().UTC()
		expires := now.Add(7 * 24 * time.Hour)
		item.Aliases = append(item.Aliases, domain.EmailIntakeAlias{Address: item.Address, TokenHash: item.InboundTokenHash, ExpiresAt: expires})
		token := randomURLToken(24)
		if item.System {
			// A reset generates a brand-new address; the previous one is disabled.
			item.LocalPart = randomIntakeLocalPart()
		} else {
			suffix := strconv.FormatInt(now.Unix()%100000, 36)
			item.LocalPart = strings.TrimSuffix(item.LocalPart, "-"+suffix) + "-" + suffix
		}
		item.Address = item.LocalPart + "@" + item.Domain
		item.InboundTokenHash = secretHash(token)
		item.UpdatedAt = now
		result = map[string]any{"address": publicEmailIntakeAddress(*item), "inboundToken": token}
		return nil
	})
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	respondMutation(w, err, http.StatusOK, nil)
}

func (s *server) deleteEmailIntakeAddress(w http.ResponseWriter, r *http.Request) {
	teamID, id := r.PathValue("id"), r.PathValue("addressId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "email_intake.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(item domain.EmailIntakeAddress) bool { return item.ID == id && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		data.EmailIntakeAddresses[index].Enabled = false
		data.EmailIntakeAddresses[index].UpdatedAt = time.Now().UTC()
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

// genericEmailDomains are common mailbox providers that never identify a
// customer, matching Linear's built-in generic domain list.
var genericEmailDomains = []string{"gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "msn.com", "yahoo.com", "ymail.com", "icloud.com", "me.com", "mac.com", "aol.com", "proton.me", "protonmail.com", "gmx.com", "gmx.net", "mail.com", "zoho.com", "yandex.com", "qq.com", "163.com", "126.com", "fastmail.com", "hey.com"}

// customerForEmailSender finds the customer whose domain matches the sender.
// Excluded senders never create requests and generic senders match no one.
func customerForEmailSender(data *domain.Bootstrap, sender string) (domain.Customer, bool) {
	sender = strings.ToLower(strings.TrimSpace(sender))
	if sender == "" {
		return domain.Customer{}, false
	}
	settings := data.WorkspaceSettings.FeatureSettings
	if customerDomainMatches(sender, settings.CustomerExcludedDomains) || customerDomainMatches(sender, settings.CustomerGenericDomains) || customerDomainMatches(sender, genericEmailDomains) {
		return domain.Customer{}, false
	}
	for _, customer := range data.Customers {
		if customerDomainMatches(sender, customer.Domains) {
			return customer, true
		}
	}
	return domain.Customer{}, false
}

func customerRequestsFeatureEnabled(data *domain.Bootstrap) bool {
	enabled, ok := data.WorkspaceSettings.FeatureFlags["customer-requests"]
	return !ok || enabled
}
