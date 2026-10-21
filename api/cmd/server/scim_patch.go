package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"flow/api/internal/store"
)

// Request bodies and PATCH handling (RFC 7644 §3.3, §3.5.2) for the shapes
// identity providers send: op names in any case ("Replace"), replace with
// no path and an object value, "active" as a boolean or the string "False",
// filtered paths (emails[type eq "work"].value, members[value eq "id"]) and
// member adds and removes in batches. Attributes Flow does not store are
// ignored so a directory's full attribute push still succeeds.

type scimPatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type scimPatchRequest struct {
	Schemas    []string             `json:"schemas"`
	Operations []scimPatchOperation `json:"Operations"`
	// A bare group resource sent with PATCH is treated as a replace.
	DisplayName string             `json:"displayName"`
	ExternalID  string             `json:"externalId"`
	Members     *[]scimMemberValue `json:"members"`
}

type scimMemberValue struct {
	Value string `json:"value"`
}

type scimUserPayload struct {
	ExternalID        string                `json:"externalId"`
	UserName          string                `json:"userName"`
	DisplayName       string                `json:"displayName"`
	Name              *store.SCIMName       `json:"name"`
	Active            any                   `json:"active"`
	Emails            []any                 `json:"emails"`
	Roles             any                   `json:"roles"`
	Title             string                `json:"title"`
	NickName          string                `json:"nickName"`
	UserType          string                `json:"userType"`
	PreferredLanguage string                `json:"preferredLanguage"`
	Locale            string                `json:"locale"`
	Timezone          string                `json:"timezone"`
	PhoneNumbers      []store.SCIMTypedItem `json:"phoneNumbers"`
	Addresses         []store.SCIMAddress   `json:"addresses"`
	Enterprise        *store.SCIMEnterprise `json:"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"`
}

type scimGroupPayload struct {
	ExternalID  string            `json:"externalId"`
	DisplayName string            `json:"displayName"`
	Members     []scimMemberValue `json:"members"`
}

func (p scimGroupPayload) memberValues() []string {
	values := make([]string, 0, len(p.Members))
	for _, member := range p.Members {
		if value := strings.TrimSpace(member.Value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// input builds a complete user write from a POST (current nil) or PUT body.
// PUT keeps the current email and active state when the body leaves them
// out.
func (p scimUserPayload) input(current *store.SCIMUser) (store.SCIMUserInput, error) {
	input := store.SCIMUserInput{ExternalID: p.ExternalID, UserName: p.UserName, DisplayName: p.DisplayName, Active: true}
	if strings.TrimSpace(input.UserName) == "" {
		return input, scimBadRequest(scimInvalidValue, "userName is required")
	}
	if p.Name != nil {
		input.Name = *p.Name
	}
	input.Profile = store.SCIMProfile{Title: p.Title, NickName: p.NickName, UserType: p.UserType, PreferredLanguage: p.PreferredLanguage, Locale: p.Locale, Timezone: p.Timezone, PhoneNumbers: p.PhoneNumbers, Addresses: p.Addresses}
	if p.Enterprise != nil {
		input.Profile.Enterprise = *p.Enterprise
	}
	if current != nil {
		input.Active, input.Email, input.AvatarURL = current.User.Active, current.User.Email, current.User.AvatarURL
	}
	if p.Active != nil {
		active, err := scimBool(p.Active)
		if err != nil {
			return input, err
		}
		input.Active = active
	}
	if email := scimPickEmail(p.Emails); email != "" {
		input.Email = email
	}
	for _, item := range p.Emails {
		if object, ok := item.(map[string]any); ok {
			raw, _ := json.Marshal(object)
			var email store.SCIMTypedItem
			if json.Unmarshal(raw, &email) == nil {
				input.Profile.Emails = append(input.Profile.Emails, email)
			}
		}
	}
	return input, nil
}

func (p scimUserPayload) role() string {
	values := scimValueList(p.Roles)
	for _, value := range values {
		if text, ok := scimString(value); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if object, ok := value.(map[string]any); ok {
			if text, ok := scimString(scimLookup(object, "display")); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

// scimPickEmail returns the primary email, else the work email, else the
// first one.
func scimPickEmail(values []any) string {
	first, work := "", ""
	for _, item := range values {
		object, ok := item.(map[string]any)
		if !ok {
			if text, isText := item.(string); isText && first == "" {
				first = text
			}
			continue
		}
		value, _ := scimString(scimLookup(object, "value"))
		if strings.TrimSpace(value) == "" {
			continue
		}
		if primary, err := scimBool(scimLookup(object, "primary")); err == nil && primary {
			return value
		}
		if kind, _ := scimString(scimLookup(object, "type")); strings.EqualFold(kind, "work") && work == "" {
			work = value
		}
		if first == "" {
			first = value
		}
	}
	if work != "" {
		return work
	}
	return first
}

func scimLookup(object map[string]any, key string) any {
	for name, value := range object {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return nil
}

// scimString reads a scalar, a {"value": x} object or a one-element list.
func scimString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case map[string]any:
		return scimString(scimLookup(typed, "value"))
	case []any:
		if len(typed) == 1 {
			return scimString(typed[0])
		}
	}
	return "", false
}

// scimBool accepts true/false and the strings "True"/"False" in any case.
func scimBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		if parsed, err := strconv.ParseBool(strings.TrimSpace(typed)); err == nil {
			return parsed, nil
		}
	case map[string]any:
		return scimBool(scimLookup(typed, "value"))
	}
	return false, scimBadRequest(scimInvalidValue, fmt.Sprintf("%v is not a boolean", value))
}

func scimValueList(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	}
	return []any{value}
}

func scimPatchOp(operation scimPatchOperation) (string, any, error) {
	kind := strings.ToLower(strings.TrimSpace(operation.Op))
	if kind != "add" && kind != "replace" && kind != "remove" {
		return "", nil, scimBadRequest(scimInvalidSyntax, fmt.Sprintf("unsupported PATCH op %q", operation.Op))
	}
	var value any
	if len(operation.Value) > 0 {
		if err := json.Unmarshal(operation.Value, &value); err != nil {
			return "", nil, scimBadRequest(scimInvalidSyntax, "PATCH value is not valid JSON")
		}
	}
	if kind != "remove" && value == nil {
		if strings.TrimSpace(operation.Path) == "" {
			return "", nil, scimBadRequest(scimInvalidValue, fmt.Sprintf("%s needs a value", kind))
		}
		// Some directories clear an attribute with replace and a null value.
		kind = "remove"
	}
	return kind, value, nil
}

// scimPatchPath splits a PATCH path into a lower-cased attribute, an
// optional value filter and an optional sub-attribute. ignore is set for
// extension attributes Flow does not store.
type scimPatchPath struct {
	attr, sub string
	filter    scimFilter
	ignore    bool
	// enterprise is set for a path in the enterprise User extension; attr
	// is then "" for the whole extension object.
	enterprise bool
}

func parseSCIMPatchPath(raw string, coreSchema string) (scimPatchPath, error) {
	path := strings.TrimSpace(raw)
	lower := strings.ToLower(path)
	enterprise := strings.ToLower(scimSchemaEnterpriseUser)
	if prefix := strings.ToLower(coreSchema) + ":"; strings.HasPrefix(lower, prefix) {
		path, lower = path[len(prefix):], lower[len(prefix):]
	} else if coreSchema == scimSchemaUser && (lower == enterprise || strings.HasPrefix(lower, enterprise+":")) {
		attr, sub, _ := strings.Cut(strings.TrimPrefix(lower[len(enterprise):], ":"), ".")
		return scimPatchPath{enterprise: true, attr: attr, sub: sub}, nil
	} else if strings.HasPrefix(lower, "urn:") {
		return scimPatchPath{ignore: true}, nil
	}
	result := scimPatchPath{}
	if open := strings.Index(path, "["); open >= 0 {
		end := strings.LastIndex(path, "]")
		if end < open {
			return result, scimBadRequest(scimInvalidPath, fmt.Sprintf("invalid path %q", raw))
		}
		filter, err := parseSCIMFilter(path[open+1 : end])
		if err != nil || filter == nil {
			return result, scimBadRequest(scimInvalidPath, fmt.Sprintf("invalid filter in path %q", raw))
		}
		result.attr, result.filter = strings.ToLower(strings.TrimSpace(path[:open])), filter
		result.sub = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(path[end+1:]), "."))
	} else {
		attr, sub, _ := strings.Cut(lower, ".")
		result.attr, result.sub = attr, sub
	}
	if result.attr == "" {
		return result, scimBadRequest(scimInvalidPath, fmt.Sprintf("invalid path %q", raw))
	}
	return result, nil
}

func scimSortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Users.

type scimUserState struct {
	input     store.SCIMUserInput
	roleValue *string
}

func scimUserStateFrom(current store.SCIMUser) *scimUserState {
	externalID := current.ExternalID
	if current.ExternalIDDefaulted {
		externalID = ""
	}
	displayName := current.User.DisplayName
	if current.NoDisplayName {
		displayName = ""
	}
	profile := current.Profile
	profile.PhoneNumbers, profile.Addresses = slices.Clone(profile.PhoneNumbers), slices.Clone(profile.Addresses)
	// The emails as the resource shows them, so a filtered path such as
	// emails[type eq "work"].value finds the account email.
	profile.Emails = nil
	for _, email := range scimUserEmails(current) {
		raw, _ := json.Marshal(email)
		var item store.SCIMTypedItem
		_ = json.Unmarshal(raw, &item)
		profile.Emails = append(profile.Emails, item)
	}
	return &scimUserState{input: store.SCIMUserInput{ExternalID: externalID, UserName: current.UserName, DisplayName: displayName, Name: current.Name, Email: current.User.Email, AvatarURL: current.User.AvatarURL, Active: current.Active(), Profile: profile}}
}

func (st *scimUserState) apply(operation scimPatchOperation) error {
	kind, value, err := scimPatchOp(operation)
	if err != nil {
		return err
	}
	if strings.TrimSpace(operation.Path) == "" {
		if kind == "remove" {
			return scimBadRequest(scimNoTarget, "remove needs a path")
		}
		object, ok := value.(map[string]any)
		if !ok {
			return scimBadRequest(scimInvalidValue, "value must be an object when path is omitted")
		}
		for _, key := range scimSortedKeys(object) {
			if err := st.set(kind, key, object[key]); err != nil {
				return err
			}
		}
		return nil
	}
	return st.set(kind, operation.Path, value)
}

func (st *scimUserState) set(kind, rawPath string, value any) error {
	path, err := parseSCIMPatchPath(rawPath, scimSchemaUser)
	if err != nil || path.ignore {
		return err
	}
	if path.enterprise {
		return st.setEnterprise(kind, path, value)
	}
	remove := kind == "remove"
	text := func() (string, error) {
		result, ok := scimString(value)
		if !ok {
			return "", scimBadRequest(scimInvalidValue, fmt.Sprintf("%s needs a string value", rawPath))
		}
		return strings.TrimSpace(result), nil
	}
	switch path.attr {
	case "active":
		if !remove {
			active, err := scimBool(value)
			if err != nil {
				return err
			}
			st.input.Active = active
		}
	case "username":
		if remove {
			return scimBadRequest(scimMutability, "userName is required")
		}
		value, err := text()
		if err != nil {
			return err
		}
		st.input.UserName = value
	case "displayname":
		if remove {
			st.input.DisplayName = ""
			return nil
		}
		value, err := text()
		if err != nil {
			return err
		}
		st.input.DisplayName = value
	case "externalid":
		if remove {
			st.input.ExternalID, st.input.ClearExternalID = "", true
			return nil
		}
		value, err := text()
		if err != nil {
			return err
		}
		st.input.ExternalID, st.input.ClearExternalID = value, false
	case "name":
		return st.setName(kind, path.sub, value)
	case "emails":
		if path.sub == "value" {
			if object, ok := value.(map[string]any); ok {
				value = scimLookup(object, "value")
			}
		}
		if err := scimPatchTypedList(&st.input.Profile.Emails, kind, path, value); err != nil {
			return err
		}
		emails := []any{}
		for _, email := range st.input.Profile.Emails {
			emails = append(emails, scimObject(email))
		}
		st.input.Email = scimPickEmail(emails)
	case "roles":
		role := ""
		if !remove {
			role = (scimUserPayload{Roles: value}).role()
		}
		st.roleValue = &role
	case "title", "nickname", "usertype", "preferredlanguage", "locale", "timezone":
		next := ""
		if !remove {
			if next, err = text(); err != nil {
				return err
			}
		}
		profile := &st.input.Profile
		fields := map[string]*string{"title": &profile.Title, "nickname": &profile.NickName, "usertype": &profile.UserType, "preferredlanguage": &profile.PreferredLanguage, "locale": &profile.Locale, "timezone": &profile.Timezone}
		*fields[path.attr] = next
	case "phonenumbers":
		return scimPatchTypedList(&st.input.Profile.PhoneNumbers, kind, path, value)
	case "addresses":
		return scimPatchTypedList(&st.input.Profile.Addresses, kind, path, value)
	}
	return nil
}

// setEnterprise applies an operation on the enterprise User extension: the
// whole extension object, one attribute, or manager / manager.value.
func (st *scimUserState) setEnterprise(kind string, path scimPatchPath, value any) error {
	enterprise := &st.input.Profile.Enterprise
	if path.attr == "" {
		if kind == "remove" {
			*enterprise = store.SCIMEnterprise{}
			return nil
		}
		object, ok := value.(map[string]any)
		if !ok {
			return scimBadRequest(scimInvalidValue, "the enterprise extension needs an object value")
		}
		if kind == "replace" {
			*enterprise = store.SCIMEnterprise{}
		}
		for _, key := range scimSortedKeys(object) {
			attr, sub, _ := strings.Cut(strings.ToLower(key), ".")
			if err := st.setEnterprise("replace", scimPatchPath{enterprise: true, attr: attr, sub: sub}, object[key]); err != nil {
				return err
			}
		}
		return nil
	}
	if path.attr == "manager" {
		return setSCIMManager(&enterprise.Manager, kind, path.sub, value)
	}
	text := ""
	if kind != "remove" {
		result, ok := scimString(value)
		if !ok {
			return scimBadRequest(scimInvalidValue, path.attr+" needs a string value")
		}
		text = strings.TrimSpace(result)
	}
	switch path.attr {
	case "employeenumber":
		enterprise.EmployeeNumber = text
	case "costcenter":
		enterprise.CostCenter = text
	case "organization":
		enterprise.Organization = text
	case "division":
		enterprise.Division = text
	case "department":
		enterprise.Department = text
	}
	return nil
}

// setSCIMManager sets the enterprise manager from an object
// ({"value": "<id>"}; an empty value clears it, as Microsoft Entra ID sends
// on removal), a bare id, or a manager.value / manager.displayName path.
func setSCIMManager(manager *store.SCIMManager, kind, sub string, value any) error {
	if kind == "remove" {
		switch sub {
		case "value":
			manager.Value = ""
		case "displayname":
			manager.DisplayName = ""
		default:
			*manager = store.SCIMManager{}
		}
		return nil
	}
	if object, ok := value.(map[string]any); ok && sub == "" {
		id, _ := scimString(scimLookup(object, "value"))
		ref, _ := scimString(scimLookup(object, "$ref"))
		display, _ := scimString(scimLookup(object, "displayName"))
		*manager = store.SCIMManager{Value: strings.TrimSpace(id), Ref: strings.TrimSpace(ref), DisplayName: strings.TrimSpace(display)}
		return nil
	}
	result, ok := scimString(value)
	if !ok {
		return scimBadRequest(scimInvalidValue, "manager needs a string or object value")
	}
	if sub == "displayname" {
		manager.DisplayName = strings.TrimSpace(result)
	} else {
		manager.Value = strings.TrimSpace(result)
	}
	return nil
}

// scimPatchTypedList applies an operation to a multi-valued complex
// attribute (phoneNumbers, addresses) through its JSON objects:
//   - without a filter, add appends values, replace replaces the list and
//     remove clears it;
//   - a value filter (phoneNumbers[type eq "work"]) selects entries; with a
//     sub-attribute only that sub-attribute changes. A replace or add that
//     matches nothing with an `attr eq "x"` filter creates the entry, as
//     Microsoft Entra ID expects for phoneNumbers[type eq "work"].value.
func scimPatchTypedList[T any](target *[]T, kind string, path scimPatchPath, value any) error {
	items := make([]map[string]any, 0, len(*target))
	for _, item := range *target {
		items = append(items, scimObject(item))
	}
	values := []map[string]any{}
	for _, item := range scimValueList(value) {
		switch typed := item.(type) {
		case map[string]any:
			values = append(values, typed)
		case string:
			values = append(values, map[string]any{"value": typed})
		}
	}
	merge := func(item map[string]any) {
		for _, object := range values {
			for key, sub := range object {
				scimSetKey(item, key, sub)
			}
		}
	}
	if path.filter == nil && path.sub == "" {
		switch kind {
		case "remove":
			items = nil
		case "replace":
			items = values
		default:
			items = append(items, values...)
		}
	} else {
		matched := false
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if path.filter != nil && !path.filter.matches(item) {
				result = append(result, item)
				continue
			}
			matched = true
			switch {
			case kind == "remove" && path.sub == "":
				continue
			case kind == "remove":
				scimSetKey(item, path.sub, nil)
			case path.sub == "":
				merge(item)
			default:
				scimSetKey(item, path.sub, value)
			}
			result = append(result, item)
		}
		if compare, ok := path.filter.(scimCompareFilter); ok && !matched && kind != "remove" && compare.op == "eq" && len(compare.path) == 1 {
			item := map[string]any{}
			scimSetKey(item, compare.path[0], compare.value)
			if path.sub != "" {
				scimSetKey(item, path.sub, value)
			} else {
				merge(item)
			}
			result = append(result, item)
		}
		items = result
	}
	raw, _ := json.Marshal(items)
	var next []T
	if err := json.Unmarshal(raw, &next); err != nil {
		return scimBadRequest(scimInvalidValue, "invalid value for "+path.attr+": "+err.Error())
	}
	*target = next
	return nil
}

// scimCanonicalKeys spells sub-attributes of phoneNumbers and addresses as
// the schema does, since PATCH paths arrive lower-cased.
var scimCanonicalKeys = map[string]string{"value": "value", "display": "display", "type": "type", "primary": "primary", "formatted": "formatted", "streetaddress": "streetAddress", "locality": "locality", "region": "region", "postalcode": "postalCode", "country": "country"}

// scimSetKey sets, or with a nil value deletes, a key compared
// case-insensitively.
func scimSetKey(object map[string]any, key string, value any) {
	for existing := range object {
		if strings.EqualFold(existing, key) {
			delete(object, existing)
		}
	}
	if canonical, ok := scimCanonicalKeys[strings.ToLower(key)]; ok {
		key = canonical
	}
	if value == nil {
		return
	}
	if text, ok := value.(string); ok && key == "primary" {
		value, _ = strconv.ParseBool(text)
	}
	object[key] = value
}

func (st *scimUserState) setName(kind, sub string, value any) error {
	name := &st.input.Name
	if sub == "" {
		if kind == "remove" {
			*name = store.SCIMName{}
			return nil
		}
		object, ok := value.(map[string]any)
		if !ok {
			return scimBadRequest(scimInvalidValue, "name needs an object value")
		}
		if kind == "replace" {
			*name = store.SCIMName{}
		}
		for _, key := range scimSortedKeys(object) {
			if err := st.setName("replace", strings.ToLower(key), object[key]); err != nil {
				return err
			}
		}
		return nil
	}
	text := ""
	if kind != "remove" {
		value, ok := scimString(value)
		if !ok {
			return scimBadRequest(scimInvalidValue, "name."+sub+" needs a string value")
		}
		text = strings.TrimSpace(value)
	}
	switch sub {
	case "givenname":
		name.GivenName = text
	case "familyname":
		name.FamilyName = text
	case "formatted":
		name.Formatted = text
	case "middlename":
		name.MiddleName = text
	case "honorificprefix":
		name.HonorificPrefix = text
	case "honorificsuffix":
		name.HonorificSuffix = text
	}
	return nil
}

// Groups.

// scimGroupPatchChange applies a group PATCH's operations, in order, to the
// group's member list and returns the net change. Unknown users in an add
// are 400 invalidValue; removing a non-member changes nothing.
func (s *server) scimGroupPatchChange(ctx context.Context, workspaceID string, group store.SCIMGroup, patch scimPatchRequest) (scimGroupChange, error) {
	if len(patch.Operations) == 0 {
		if patch.DisplayName == "" && patch.ExternalID == "" && patch.Members == nil {
			return scimGroupChange{}, scimBadRequest(scimInvalidSyntax, "PATCH needs Operations")
		}
		change := scimGroupChange{displayName: patch.DisplayName, externalID: patch.ExternalID}
		if patch.Members != nil {
			change.replaceMembers = true
			change.members = scimGroupPayload{Members: *patch.Members}.memberValues()
		}
		return change, nil
	}
	externalIDs := map[string]string{}
	original := make([]string, 0, len(group.Members))
	desired := map[string]bool{}
	for _, member := range group.Members {
		original = append(original, member.UserID)
		desired[member.UserID] = true
		externalIDs[member.UserID] = member.ExternalID
	}
	order := append([]string{}, original...)
	resolve := func(value string, strict bool) (string, error) {
		item, err := s.store.SCIMUser(ctx, workspaceID, value)
		if err == nil {
			externalIDs[item.User.ID] = item.ExternalID
			return item.User.ID, nil
		}
		if errors.Is(err, store.ErrSCIMNotFound) {
			if strict {
				return "", fmt.Errorf("%w: %q", store.ErrSCIMMemberNotFound, value)
			}
			return "", nil
		}
		return "", err
	}
	memberValues := func(value any) []string {
		values := []string{}
		for _, item := range scimValueList(value) {
			if text, ok := scimString(item); ok && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
		return values
	}
	change := scimGroupChange{}
	var apply func(kind, rawPath string, value any) error
	apply = func(kind, rawPath string, value any) error {
		path, err := parseSCIMPatchPath(rawPath, scimSchemaGroup)
		if err != nil || path.ignore {
			return err
		}
		switch path.attr {
		case "displayname":
			if kind != "remove" {
				text, ok := scimString(value)
				if !ok || strings.TrimSpace(text) == "" {
					return scimBadRequest(scimInvalidValue, "displayName needs a string value")
				}
				change.displayName = strings.TrimSpace(text)
			}
		case "externalid":
			if kind == "remove" {
				change.externalID, change.clearExternalID = "", true
			} else if text, ok := scimString(value); ok {
				change.externalID, change.clearExternalID = strings.TrimSpace(text), false
			}
		case "members":
			if path.filter != nil {
				if kind != "remove" {
					return scimBadRequest(scimInvalidPath, "a member filter path is only supported with remove")
				}
				for userID := range desired {
					if path.filter.matches(map[string]any{"value": userID}) || (externalIDs[userID] != "" && path.filter.matches(map[string]any{"value": externalIDs[userID]})) {
						delete(desired, userID)
					}
				}
				return nil
			}
			if kind == "replace" || (kind == "remove" && value == nil) {
				clear(desired)
			}
			for _, raw := range memberValues(value) {
				userID, err := resolve(raw, kind != "remove")
				if err != nil {
					return err
				}
				if userID == "" {
					continue
				}
				if kind == "remove" {
					delete(desired, userID)
					continue
				}
				if !desired[userID] {
					desired[userID] = true
					order = append(order, userID)
				}
			}
		}
		return nil
	}
	for _, operation := range patch.Operations {
		kind, value, err := scimPatchOp(operation)
		if err != nil {
			return scimGroupChange{}, err
		}
		if strings.TrimSpace(operation.Path) == "" {
			if kind == "remove" {
				return scimGroupChange{}, scimBadRequest(scimNoTarget, "remove needs a path")
			}
			object, ok := value.(map[string]any)
			if !ok {
				return scimGroupChange{}, scimBadRequest(scimInvalidValue, "value must be an object when path is omitted")
			}
			for _, key := range scimSortedKeys(object) {
				if err := apply(kind, key, object[key]); err != nil {
					return scimGroupChange{}, err
				}
			}
			continue
		}
		if err := apply(kind, operation.Path, value); err != nil {
			return scimGroupChange{}, err
		}
	}
	before := map[string]bool{}
	for _, userID := range original {
		before[userID] = true
		if !desired[userID] {
			change.remove = append(change.remove, userID)
		}
	}
	for _, userID := range order {
		if desired[userID] && !before[userID] {
			change.add = append(change.add, userID)
			before[userID] = true
		}
	}
	return change, nil
}
