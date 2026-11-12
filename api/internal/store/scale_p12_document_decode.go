package store

import (
	"encoding/json"

	"flow/api/internal/domain"
)

// DecodeRealtimeDocument applies the same current-revision marker expansion
// as the installed read model. Bound expansion before invoking that decoder.
func DecodeRealtimeDocument(raw json.RawMessage) (domain.Document, error) {
	var document domain.Document
	if len(raw) > MetadataRecordPageMaxBytes {
		return document, ErrEventSourceBudget
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return document, err
	}
	var revisions []map[string]json.RawMessage
	if err := json.Unmarshal(fields["revisions"], &revisions); len(fields["revisions"]) > 0 && err != nil {
		return document, err
	}
	bodyBytes := 0
	for _, name := range revisionBodyFields {
		bodyBytes += len(fields[name]) + len(name) + 4
	}
	expanded := len(raw)
	for _, revision := range revisions {
		var marker map[string]json.RawMessage
		if json.Unmarshal(revision[offloadMarker], &marker) == nil && jsonString(marker["body"]) == "document" {
			expanded += bodyBytes
		}
		if expanded > 1<<20 {
			return document, ErrEventSourceBudget
		}
	}
	decoded, err := resolveDocumentMarkers("documents", raw)
	if err != nil {
		return document, err
	}
	err = json.Unmarshal(decoded, &document)
	return document, err
}
