package store

import (
	"encoding/json"
	"strings"
)

// SCIMProfile holds the User attributes beyond name and email that
// identity providers send by default (RFC 7643 §4.1 core extras and the
// enterprise extension of §4.3). Flow keeps them with the SCIM identity
// (claims_json) and returns them as they were written.
type SCIMProfile struct {
	Title             string `json:"title,omitempty"`
	NickName          string `json:"nickName,omitempty"`
	UserType          string `json:"userType,omitempty"`
	PreferredLanguage string `json:"preferredLanguage,omitempty"`
	Locale            string `json:"locale,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	// Emails is the emails list as the IdP sent it; the account email is
	// the primary (else work, else first) value.
	Emails       []SCIMTypedItem `json:"emails,omitempty"`
	PhoneNumbers []SCIMTypedItem `json:"phoneNumbers,omitempty"`
	Addresses    []SCIMAddress   `json:"addresses,omitempty"`
	Enterprise   SCIMEnterprise  `json:"enterprise,omitzero"`
	// ActiveUnset is set by PATCH remove of "active" (RFC 7644 §3.5.2.2):
	// the resource then omits the attribute. The membership keeps its
	// state; any later write of active clears it.
	ActiveUnset bool `json:"activeUnset,omitempty"`
}

// SCIMTypedItem is a multi-valued string attribute value (emails,
// phoneNumbers). Primary is kept as sent, including an explicit false.
type SCIMTypedItem struct {
	Value   string `json:"value,omitempty"`
	Display string `json:"display,omitempty"`
	Type    string `json:"type,omitempty"`
	Primary *bool  `json:"primary,omitempty"`
}

// SCIMAddress is one value of the addresses attribute.
type SCIMAddress struct {
	Formatted     string `json:"formatted,omitempty"`
	StreetAddress string `json:"streetAddress,omitempty"`
	Locality      string `json:"locality,omitempty"`
	Region        string `json:"region,omitempty"`
	PostalCode    string `json:"postalCode,omitempty"`
	Country       string `json:"country,omitempty"`
	Type          string `json:"type,omitempty"`
	Primary       *bool  `json:"primary,omitempty"`
}

// SCIMEnterprise is the enterprise User extension
// (urn:ietf:params:scim:schemas:extension:enterprise:2.0:User).
type SCIMEnterprise struct {
	EmployeeNumber string      `json:"employeeNumber,omitempty"`
	CostCenter     string      `json:"costCenter,omitempty"`
	Organization   string      `json:"organization,omitempty"`
	Division       string      `json:"division,omitempty"`
	Department     string      `json:"department,omitempty"`
	Manager        SCIMManager `json:"manager,omitzero"`
}

// SCIMManager is the enterprise manager attribute. Directories send it as
// an object ({"value": "<id>"}) or, in PATCH values, as the bare id.
type SCIMManager struct {
	Value       string `json:"value,omitempty"`
	Ref         string `json:"$ref,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

func (m *SCIMManager) UnmarshalJSON(raw []byte) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*m = SCIMManager{Value: strings.TrimSpace(text)}
		return nil
	}
	type plain SCIMManager
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*m = SCIMManager(value)
	return nil
}

// IsZero reports whether no extension attribute is set.
func (e SCIMEnterprise) IsZero() bool {
	return e == SCIMEnterprise{}
}

func normalizeSCIMProfile(profile SCIMProfile) SCIMProfile {
	trim := func(values ...*string) {
		for _, value := range values {
			*value = strings.TrimSpace(*value)
		}
	}
	trim(&profile.Title, &profile.NickName, &profile.UserType, &profile.PreferredLanguage, &profile.Locale, &profile.Timezone)
	enterprise := &profile.Enterprise
	trim(&enterprise.EmployeeNumber, &enterprise.CostCenter, &enterprise.Organization, &enterprise.Division, &enterprise.Department, &enterprise.Manager.Value, &enterprise.Manager.Ref, &enterprise.Manager.DisplayName)
	if enterprise.Manager.Value == "" {
		enterprise.Manager = SCIMManager{}
	}
	typed := func(items []SCIMTypedItem) []SCIMTypedItem {
		result := items[:0:0]
		for _, item := range items {
			trim(&item.Value, &item.Display, &item.Type)
			if item.Value != "" {
				result = append(result, item)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}
	profile.Emails, profile.PhoneNumbers = typed(profile.Emails), typed(profile.PhoneNumbers)
	addresses := profile.Addresses[:0:0]
	for _, address := range profile.Addresses {
		trim(&address.Formatted, &address.StreetAddress, &address.Locality, &address.Region, &address.PostalCode, &address.Country, &address.Type)
		if address != (SCIMAddress{Type: address.Type, Primary: address.Primary}) {
			addresses = append(addresses, address)
		}
	}
	profile.Addresses = addresses
	if len(profile.Addresses) == 0 {
		profile.Addresses = nil
	}
	return profile
}
