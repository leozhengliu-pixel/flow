package store

import (
	"maps"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func commentActionAuthorizationFields(base map[reflect.Type][]string) map[reflect.Type][]string {
	fields := maps.Clone(base)
	issue := reflect.TypeFor[domain.Issue]()
	fields[issue] = append(slices.Clone(fields[issue]), "Priority", "Attachments")
	fields[reflect.TypeFor[domain.Attachment]()] = []string{"URL"}
	return fields
}

var issueAuthorizationCommentActionFields = commentActionAuthorizationFields(issueAuthorizationFields)
var issueAuthorizationFullCommentActionFields = commentActionAuthorizationFields(issueAuthorizationFullPermissionFields)

var issueAuthorizationRecurrenceTimingFields = func() map[reflect.Type][]string {
	fields := maps.Clone(issueAuthorizationFields)
	issue := reflect.TypeFor[domain.Issue]()
	fields[issue] = append(slices.Clone(fields[issue]), "Recurrence", "DueDate", "NextOccurrenceAt")
	return fields
}()

func (r *IssueWriteAuthorizationRead) projectionFields() map[reflect.Type][]string {
	if r.recurrenceTiming {
		return issueAuthorizationRecurrenceTimingFields
	}
	if r.commentActionValidation {
		if r.fullPermissions {
			return issueAuthorizationFullCommentActionFields
		}
		return issueAuthorizationCommentActionFields
	}
	if r.fullPermissions {
		return issueAuthorizationFullPermissionFields
	}
	return issueAuthorizationFields
}
