package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// Insights configuration shared by saved views and team issue views
// ("Set default for everyone"). The web client owns rendering; the server
// only accepts the documented shape so a shared default can never break
// everyone's panel.
var (
	insightMeasures     = []string{"issueCount", "cycleTime", "leadTime", "issueAge", "timeInStatus"}
	insightDimensions   = []string{"status", "statusType", "assignee", "agent", "agentSession", "creator", "priority", "label", "customer", "template", "externalSource", "project", "initiative", "projectLabel", "cycle", "addedToCycle", "createdDate", "completedDate", "canceledDate", "startedDate", "dueDate", "burnUp"}
	insightAggregations = []string{"average", "p25", "median", "p75", "p95", "min", "max"}
)

const maxInsightsConfigBytes = 8 << 10

func validInsightDimension(value string) bool {
	if slices.Contains(insightDimensions, value) {
		return true
	}
	for _, prefix := range []string{"labelGroup:", "projectLabelGroup:"} {
		if id, ok := strings.CutPrefix(value, prefix); ok {
			return id != "" && len(id) <= 128
		}
	}
	return false
}

// normalizeInsightsConfig validates an Insights configuration object and
// returns its canonical JSON. Unknown keys and out-of-range values are
// rejected.
func normalizeInsightsConfig(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) > maxInsightsConfigBytes {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config struct {
		Measure          *string   `json:"measure,omitempty"`
		TimeInStatusIDs  *[]string `json:"timeInStatusIds,omitempty"`
		Slice            *string   `json:"slice,omitempty"`
		Segment          *string   `json:"segment,omitempty"`
		ShowArchived     *bool     `json:"showArchived,omitempty"`
		HideEmptySegment *bool     `json:"hideEmptySegment,omitempty"`
		HideEmptySlice   *bool     `json:"hideEmptySlice,omitempty"`
		// HideUnknownCustomer is Linear's "Hide Unknown customer" for a Customer slice or segment.
		HideUnknownCustomer *bool     `json:"hideUnknownCustomer,omitempty"`
		Colors              *string   `json:"colors,omitempty"`
		Aggregation         *string   `json:"aggregation,omitempty"`
		Aggregations        *[]string `json:"aggregations,omitempty"`
		LatencyScale        *string   `json:"latencyScale,omitempty"`
	}
	if err := decoder.Decode(&config); err != nil || decoder.More() {
		return nil, false
	}
	if config.Measure != nil && !slices.Contains(insightMeasures, *config.Measure) {
		return nil, false
	}
	if config.TimeInStatusIDs != nil {
		if len(*config.TimeInStatusIDs) > 64 {
			return nil, false
		}
		for _, id := range *config.TimeInStatusIDs {
			if id == "" || len(id) > 128 {
				return nil, false
			}
		}
	}
	if config.Slice != nil && !validInsightDimension(*config.Slice) {
		return nil, false
	}
	if config.Segment != nil && *config.Segment != "none" && !validInsightDimension(*config.Segment) {
		return nil, false
	}
	if config.Colors != nil && *config.Colors != "status" && *config.Colors != "auto" {
		return nil, false
	}
	if config.Aggregation != nil && !slices.Contains(insightAggregations, *config.Aggregation) {
		return nil, false
	}
	if config.Aggregations != nil {
		if len(*config.Aggregations) == 0 || len(*config.Aggregations) > len(insightAggregations) {
			return nil, false
		}
		for _, aggregation := range *config.Aggregations {
			if !slices.Contains(insightAggregations, aggregation) {
				return nil, false
			}
		}
	}
	if config.LatencyScale != nil && *config.LatencyScale != "linear" && *config.LatencyScale != "log" {
		return nil, false
	}
	normalized, err := json.Marshal(config)
	if err != nil {
		return nil, false
	}
	return normalized, true
}
