package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// ETags and sorting (RFC 7644 §3.14 and §3.4.2.3).

// scimWeakVersion is a weak entity tag over the parts that identify a
// resource version.
func scimWeakVersion(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// scimUserVersion changes whenever the user resource can: every SCIM write
// stamps the account's updated_at, and active also follows the membership.
func scimUserVersion(item store.SCIMUser) string {
	return scimWeakVersion(item.User.ID, item.UpdatedAt.UTC().Format(time.RFC3339Nano), item.MemberStatus)
}

// scimGroupVersion changes with every attribute or member change (both
// stamp the group's updated_at).
func scimGroupVersion(group store.SCIMGroup) string {
	return scimWeakVersion(group.ID, group.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

// scimTeamVersion versions a team that no SCIM group is linked to yet (it
// is read-only until a write links it).
func scimTeamVersion(team domain.Team) string {
	updated := ""
	if team.UpdatedAt != nil {
		updated = team.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return scimWeakVersion(team.ID, team.Name, updated)
}

// scimETagMatches compares entity tags weakly, as SCIM clients send the weak
// tags they read with If-Match. "*" matches any current version.
func scimETagMatches(header, version string) bool {
	opaque := func(tag string) string { return strings.TrimPrefix(strings.TrimSpace(tag), "W/") }
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || (tag != "" && opaque(tag) == opaque(version)) {
			return true
		}
	}
	return false
}

// scimPreconditionFailed answers 412 when If-Match names a version other than
// the current one. Writes then go ahead only on the version the client read.
func scimPreconditionFailed(w http.ResponseWriter, r *http.Request, version string) bool {
	header := strings.TrimSpace(r.Header.Get("If-Match"))
	if header == "" || scimETagMatches(header, version) {
		return false
	}
	w.Header().Set("ETag", version)
	writeSCIMError(w, http.StatusPreconditionFailed, "", "Failed to update. Resource has changed on the server (If-Match "+header+", current "+version+").")
	return true
}

// scimNotModified answers 304 to a GET whose If-None-Match names the current
// version.
func scimNotModified(w http.ResponseWriter, r *http.Request, version string) bool {
	header := strings.TrimSpace(r.Header.Get("If-None-Match"))
	if header == "" || !scimETagMatches(header, version) {
		return false
	}
	w.Header().Set("ETag", version)
	w.WriteHeader(http.StatusNotModified)
	return true
}

// writeSCIMResource writes a single resource with its ETag header.
func writeSCIMResource(w http.ResponseWriter, status int, resource map[string]any, version string) {
	if version != "" {
		w.Header().Set("ETag", version)
	}
	writeSCIM(w, status, resource)
}

// scimSortKey is the value a resource sorts by: the first value the path
// reaches, preferring a multi-valued attribute's primary value.
func scimSortKey(resource map[string]any, path []string) (any, bool) {
	values := scimResolve(resource, path)
	if len(path) > 0 {
		if parent := scimResolve(resource, path[:1]); len(parent) > 1 {
			for _, item := range parent {
				object, ok := item.(map[string]any)
				if primary, _ := scimLookup(object, "primary").(bool); ok && primary {
					if resolved := scimResolve(object, path[1:]); len(resolved) > 0 {
						values = resolved
					}
					break
				}
			}
		}
	}
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			value = scimLookup(object, "value")
		}
		if value != nil && value != "" {
			return value, true
		}
	}
	return nil, false
}

// scimCompareSortKeys orders two present sort values: date-times by time,
// numbers numerically, booleans false first, strings case-insensitively
// unless the attribute is case-exact.
func scimCompareSortKeys(a, b any, full string) int {
	switch left := a.(type) {
	case float64:
		if right, ok := b.(float64); ok {
			return scimCompareFloat(left, right)
		}
	case bool:
		if right, ok := b.(bool); ok {
			switch {
			case left == right:
				return 0
			case !left:
				return -1
			}
			return 1
		}
	}
	left, right := scimSortText(a), scimSortText(b)
	if scimDateTimePaths[full] {
		leftTime, leftErr := time.Parse(time.RFC3339Nano, left)
		rightTime, rightErr := time.Parse(time.RFC3339Nano, right)
		if leftErr == nil && rightErr == nil {
			return leftTime.Compare(rightTime)
		}
	}
	if !scimCaseExactPaths[full] {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	return strings.Compare(left, right)
}

func scimSortText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	}
	return ""
}

// sortSCIMResources orders resources by params.sortPath. Resources without
// a value sort last ascending and first descending; ties keep their order.
func sortSCIMResources(resources []map[string]any, params scimListParams) {
	if len(params.sortPath) == 0 {
		return
	}
	full := strings.Join(params.sortPath, ".")
	type keyed struct {
		resource map[string]any
		key      any
		present  bool
	}
	items := make([]keyed, len(resources))
	for index, resource := range resources {
		key, present := scimSortKey(scimFilterable(resource), params.sortPath)
		items[index] = keyed{resource, key, present}
	}
	slices.SortStableFunc(items, func(a, b keyed) int {
		result := 0
		switch {
		case !a.present && !b.present:
			return 0
		case !a.present:
			result = 1
		case !b.present:
			result = -1
		default:
			result = scimCompareSortKeys(a.key, b.key, full)
		}
		if params.descending {
			return -result
		}
		return result
	})
	for index := range items {
		resources[index] = items[index].resource
	}
}

// scimUserSQLSort maps a sortBy path to the column ListSCIMUsers orders by
// in SQL; other attributes are sorted in memory.
func scimUserSQLSort(path []string) (string, bool) {
	switch strings.Join(path, ".") {
	case "", "username":
		return "userName", true
	case "displayname":
		return "displayName", true
	case "externalid":
		return "externalId", true
	case "emails", "emails.value":
		return "email", true
	case "id":
		return "id", true
	case "meta.created":
		return "created", true
	case "meta.lastmodified":
		return "lastModified", true
	}
	return "", false
}
