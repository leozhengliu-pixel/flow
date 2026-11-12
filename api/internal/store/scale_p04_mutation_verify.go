package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

var narrowMutationComparisons atomic.Uint64
var narrowMutationDifferences atomic.Uint64

type mutationResultKey struct{}

// ContextWithMutationResults identifies response values assigned by a callback.
func ContextWithMutationResults(ctx context.Context, results ...any) context.Context {
	if !narrowMutationInputs() || !verifyNarrowMutationInputs() {
		return ctx
	}
	return context.WithValue(ctx, mutationResultKey{}, results)
}

// NarrowMutationVerificationCounts reports completed runtime comparisons.
func NarrowMutationVerificationCounts() (comparisons, differences uint64) {
	return narrowMutationComparisons.Load(), narrowMutationDifferences.Load()
}

func verifyNarrowMutationInputs() bool {
	if testing.Testing() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_NARROW_MUTATION_VERIFY"))) {
	case "1", "true", "on":
		return true
	}
	return false
}

type mutationCallbackResult struct {
	Data            domain.Bootstrap       `json:"data"`
	NextIssueNumber int                    `json:"nextIssueNumber"`
	Aggregate       string                 `json:"aggregate"`
	ErrorType       string                 `json:"errorType"`
	Error           string                 `json:"error"`
	Panic           string                 `json:"panic"`
	Payload         json.RawMessage        `json:"payload"`
	Audit           []domain.AuditLogEntry `json:"audit"`
	Attribution     *domain.ActorBot       `json:"attribution"`
	ViewerTeams     []string               `json:"viewerTeams"`
	Results         []json.RawMessage      `json:"results"`
}

func cloneCallbackInput(data domain.Bootstrap) domain.Bootstrap {
	owned := cloneBootstrap(data)
	owned.NextIssueNumber = data.NextIssueNumber
	owned.Viewer = mutationInputActor(data.Viewer)
	owned.ViewerTeamIDs = slices.Clone(data.ViewerTeamIDs)
	for i := range owned.Users {
		if data.Users[i].Via != nil {
			owned.Users[i] = mutationInputActor(data.Users[i])
		}
	}
	return owned
}

// fullCallbackInput supplies every metadata field, retaining the same scoped
// SQL records and authoritative request identity as the production callback.
func fullCallbackInput(stored, input domain.Bootstrap, fields []string) domain.Bootstrap {
	full := cloneCallbackInput(collectionMetadata(stored))
	loaded := cloneCallbackInput(input)
	source, target := reflect.ValueOf(&loaded).Elem(), reflect.ValueOf(&full).Elem()
	for _, field := range persistedMetadataFields() {
		if slices.Contains(fields, field.name) {
			target.Field(field.index).Set(source.Field(field.index))
		}
	}
	if !slices.Contains(fields, "projects") {
		for _, project := range loaded.Projects {
			if index := slices.IndexFunc(full.Projects, func(item domain.Project) bool { return item.ID == project.ID }); index >= 0 {
				full.Projects[index] = project
			}
		}
	}
	full.Issues, full.Comments, full.Activities = loaded.Issues, loaded.Comments, loaded.Activities
	full.Notifications, full.NotificationDeliveries = loaded.Notifications, loaded.NotificationDeliveries
	full.Viewer, full.ViewerRole = loaded.Viewer, loaded.ViewerRole
	full.NextIssueNumber, full.ViewerTeamIDs = loaded.NextIssueNumber, loaded.ViewerTeamIDs
	return full
}

func runVerifiedCallback(data *domain.Bootstrap, payload any, audit *auditCollector, results []any, mutate func(*domain.Bootstrap) (string, error)) (result mutationCallbackResult, err error, panicked any) {
	defer func() {
		panicked = recover()
		result.Data = *data
		result.NextIssueNumber = data.NextIssueNumber
		result.Attribution, result.ViewerTeams = data.Viewer.Via, data.ViewerTeamIDs
		if err != nil {
			result.ErrorType, result.Error = fmt.Sprintf("%T", err), err.Error()
			// Failed callbacks persist no records or side-effect intents.
			result.Data = domain.Bootstrap{}
			result.NextIssueNumber, result.Attribution, result.ViewerTeams = 0, nil, nil
		}
		if panicked != nil {
			result.Panic = fmt.Sprintf("%T: %v", panicked, panicked)
		}
		if raw, marshalErr := json.Marshal(payload); marshalErr == nil {
			result.Payload = raw
		} else {
			result.Payload, _ = json.Marshal(fmt.Sprintf("%T: %v", marshalErr, marshalErr))
		}
		if audit != nil && err == nil {
			result.Audit = cloneSnapshotValue(reflect.ValueOf(audit.pending)).Interface().([]domain.AuditLogEntry)
		}
		for _, value := range results {
			raw, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				raw, _ = json.Marshal(fmt.Sprintf("%T: %v", marshalErr, marshalErr))
			}
			result.Results = append(result.Results, raw)
		}
	}()
	result.Aggregate, err = mutate(data)
	return
}

// Verification executes callbacks on independent owned inputs. Only the full
// execution is allowed to reach persistence, response captures and publishers.
func (s *SQLiteStore) verifyMutationCallback(ctx context.Context, tx *sqlTx, kind string, stored domain.Bootstrap, data *domain.Bootstrap, fields []string, payload any, mutate func(*domain.Bootstrap) (string, error)) (string, error) {
	return s.compareMutationCallback(ctx, tx, kind, stored, data, fields, payload, mutate, testing.Testing(), true)
}

func (s *SQLiteStore) compareMutationCallback(ctx context.Context, tx *sqlTx, kind string, stored domain.Bootstrap, data *domain.Bootstrap, fields []string, payload any, mutate func(*domain.Bootstrap) (string, error), panicOnDifference, record bool) (string, error) {
	before := fullCallbackInput(stored, *data, fields)
	full := cloneCallbackInput(before)
	narrow := cloneCallbackInput(*data)
	prepare := func(input *domain.Bootstrap) {
		input.FormerMembers = s.formerMemberLookup(ctx, tx, input)
		input.CollaborationUpdates = collaborationUpdatesLookup(ctx, tx, stored.Workspace.URLKey)
	}
	prepare(&narrow)
	prepare(&full)
	protected, _ := json.Marshal(struct {
		Data    domain.Bootstrap
		Payload any
	}{before, payload})
	audit := auditCollectorFrom(ctx)
	results, _ := ctx.Value(mutationResultKey{}).([]any)
	first, _, _ := runVerifiedCallback(&narrow, payload, audit, results, mutate)
	if first.ErrorType == "" {
		restoreUnlistedMetadata(&first.Data, before, fields)
	}
	second, err, secondPanic := runVerifiedCallback(&full, payload, audit, results, mutate)
	var comparison uint64
	if record {
		comparison = narrowMutationComparisons.Add(1)
	}
	path := compareMutationCallbacks(first, second, protected)
	if path != "" {
		if record {
			narrowMutationDifferences.Add(1)
		}
		slog.Error("P04 mutation verification mismatch; using full callback result", "kind", kind, "field", path, "comparison", comparison)
		if panicOnDifference {
			panic("P04 mutation verification mismatch: " + kind + " at " + path)
		}
	} else if record {
		slog.Info("P04 mutation verified", "kind", kind, "comparison", comparison)
	}
	*data = full
	if secondPanic != nil {
		panic(secondPanic)
	}
	return second.Aggregate, err
}

var mutationVerifyStamp = regexp.MustCompile(`[0-9]{12,}(?:_[0-9a-z]{10,16})?`)

// Existing identifiers/clocks remain exact. New stamps receive distinct names
// in deterministic traversal order, preserving references and map cardinality.
func normalizedMutationResult(result mutationCallbackResult, protected []byte) any {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("cannot encode callback result: %T: %v", err, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Sprintf("cannot decode callback result: %v", err)
	}
	known := map[string]bool{}
	for _, stamp := range mutationVerifyStamp.FindAll(protected, -1) {
		known[string(stamp)] = true
	}
	identifiers := map[string]string{}
	protectedClocks := map[string]bool{}
	text := func(s string) string {
		if _, err := time.Parse(time.RFC3339Nano, s); err == nil {
			present, checked := protectedClocks[s]
			if !checked {
				present = bytes.Contains(protected, []byte(s))
				protectedClocks[s] = present
			}
			if !present {
				return "<callback clock>"
			}
		}
		return mutationVerifyStamp.ReplaceAllStringFunc(s, func(stamp string) string {
			if known[stamp] {
				return stamp
			}
			if id, ok := identifiers[stamp]; ok {
				return id
			}
			id := fmt.Sprintf("<callback id %d>", len(identifiers))
			identifiers[stamp] = id
			return id
		})
	}
	var walk func(any, string) any
	walk = func(v any, path string) any {
		switch v := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			result := make(map[string]any, len(v))
			for _, key := range keys {
				result[text(key)] = walk(v[key], path+"."+key)
			}
			return result
		case []any:
			for i := range v {
				v[i] = walk(v[i], path+"[]")
			}
			if path == ".data.notifications" || path == ".data.notificationDeliveries" || path == ".data.auditLog" || path == ".audit" {
				sort.Slice(v, func(i, j int) bool {
					a, _ := json.Marshal(v[i])
					b, _ := json.Marshal(v[j])
					return bytes.Compare(a, b) < 0
				})
			}
			return v
		case string:
			return text(v)
		default:
			return v
		}
	}
	return walk(value, "")
}

func compareMutationCallbacks(narrow, full mutationCallbackResult, protected []byte) string {
	a, b := normalizedMutationResult(narrow, protected), normalizedMutationResult(full, protected)
	var difference func(any, any, string) string
	difference = func(a, b any, path string) string {
		if reflect.DeepEqual(a, b) {
			return ""
		}
		switch a := a.(type) {
		case map[string]any:
			if b, ok := b.(map[string]any); ok && len(a) == len(b) {
				keys := make([]string, 0, len(a))
				for key := range a {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if next := difference(a[key], b[key], path+"."+key); next != "" {
						return next
					}
				}
			}
		case []any:
			if b, ok := b.([]any); ok && len(a) == len(b) {
				for i := range a {
					if next := difference(a[i], b[i], fmt.Sprintf("%s[%d]", path, i)); next != "" {
						return next
					}
				}
			}
		}
		return path
	}
	return difference(a, b, "callback")
}
