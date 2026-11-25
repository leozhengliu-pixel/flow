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
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type metadataRecordVerificationKey struct{}

// WithMetadataRecordVerification opts a migrated callback into the full-input
// oracle. Results are response captures and other callback-owned values that
// must be reset before the oracle executes. Publishers run only after it.
func WithMetadataRecordVerification(ctx context.Context, results ...any) context.Context {
	ctx = context.WithValue(ctx, metadataRecordVerificationKey{}, true)
	return context.WithValue(ctx, mutationResultKey{}, results)
}

func metadataRecordVerificationEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(metadataRecordVerificationKey{}).(bool)
	if !enabled {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_NARROW_MUTATION_VERIFY"))) {
	case "0", "false", "off":
		return false
	case "1", "true", "on":
		return true
	}
	return testing.Testing()
}

func metadataRecordOracleFields() []string {
	fields := make([]string, 0, len(persistedMetadataFields()))
	for _, field := range persistedMetadataFields() {
		fields = append(fields, field.name)
	}
	return fields
}

func verifiedMetadataRecordMutationAdapter(ctx context.Context, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) func(*domain.Bootstrap) (string, error) {
	narrowContext := context.WithValue(ctx, metadataRecordVerificationKey{}, false)
	narrowCallback := metadataRecordMutationAdapter(narrowContext, mutate)
	return func(data *domain.Bootstrap) (string, error) {
		owned := func() domain.Bootstrap {
			value := cloneCallbackInput(*data)
			value.FormerMembers, value.CollaborationUpdates = data.FormerMembers, data.CollaborationUpdates
			return value
		}
		narrow, full := owned(), owned()
		protected, _ := json.Marshal(struct {
			Data    domain.Bootstrap
			Payload any
		}{*data, payload})
		results, _ := ctx.Value(mutationResultKey{}).([]any)
		originals := make([]reflect.Value, len(results))
		for i, result := range results {
			value := reflect.ValueOf(result)
			if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
				originals[i] = cloneSnapshotValue(value.Elem())
			}
		}
		first, _, _ := runVerifiedCallback(&narrow, payload, nil, results, narrowCallback)
		first.Data = cloneCallbackInput(first.Data)
		for i, original := range originals {
			if original.IsValid() {
				resetMetadataMutationCapture(reflect.ValueOf(results[i]).Elem(), original)
			}
		}
		second, err, panicked := runVerifiedCallback(&full, payload, nil, results, mutate)
		// AuditLog is a staging buffer, not a persisted metadata field. The
		// outer collector commits only additions, independently of whether a
		// callback saw historical entries or an empty staging slice.
		for _, result := range []*mutationCallbackResult{&first, &second} {
			result.Audit = stagedAuditEntries(data.AuditLog, result.Data.AuditLog)
			if len(result.Audit) == 0 {
				result.Audit = nil
			}
			result.Data.AuditLog = nil
		}
		comparison := narrowMutationComparisons.Add(1)
		if path := compareMetadataRecordCallbacks(first, second, protected); path != "" {
			narrowMutationDifferences.Add(1)
			slog.Error("P16 record mutation verification mismatch; using full callback result", "event", event, "field", path, "comparison", comparison)
			if testing.Testing() {
				panic("P16 record mutation verification mismatch: " + event + " at " + path)
			}
		}
		*data = full
		if panicked != nil {
			panic(panicked)
		}
		return second.Aggregate, err
	}
}

var metadataMutationSlug = regexp.MustCompile(`-[a-f0-9]{12}`)

func compareMetadataRecordCallbacks(first, second mutationCallbackResult, protected []byte) string {
	// Creation generates a fresh suffix in addition to the IDs and clocks
	// handled by P04. Existing suffixes and anything in the input stay exact.
	var slugs func(any) any
	slugs = func(value any) any {
		switch v := value.(type) {
		case string:
			return metadataMutationSlug.ReplaceAllStringFunc(v, func(suffix string) string {
				if bytes.Contains(protected, []byte(suffix)) {
					return suffix
				}
				return "-<callback slug>"
			})
		case map[string]any:
			for key, item := range v {
				v[key] = slugs(item)
			}
		case []any:
			for i, item := range v {
				v[i] = slugs(item)
			}
		}
		return value
	}
	// An all-digit suffix would otherwise be interpreted as a generated ID
	// by P04. Preserve it through that pass, then normalize it as a slug.
	stamps := slices.Clone(protected)
	for _, result := range []mutationCallbackResult{first, second} {
		raw, _ := json.Marshal(result)
		for _, suffix := range metadataMutationSlug.FindAll(raw, -1) {
			stamps = append(append(stamps, ' '), suffix...)
		}
	}
	a, b := slugs(normalizedMutationResult(first, stamps)), slugs(normalizedMutationResult(second, stamps))
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
				slices.Sort(keys)
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

// Preserve pointer aliases held by the persisted payload (for example a
// child-event envelope) when resetting the closure's captures.
func resetMetadataMutationCapture(value, original reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() && !original.IsNil() {
			resetMetadataMutationCapture(value.Elem(), original.Elem())
			return
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if !value.Field(i).CanSet() {
				value.Set(original)
				return
			}
		}
		for i := 0; i < value.NumField(); i++ {
			resetMetadataMutationCapture(value.Field(i), original.Field(i))
		}
		return
	}
	value.Set(original)
}
