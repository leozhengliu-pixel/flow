package store

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
)

// Exact equality is already a complete comparison. Avoid JSON decoding and
// stamp normalization of those same immutable collections in both results.
// Leave unequal fields intact for the existing generated-ID/clock oracle.
func scaleP16BoundVerificationComparison(a, b mutationCallbackResult, protected []byte) (mutationCallbackResult, mutationCallbackResult, []byte) {
	enabled := func(name string) bool {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
		case "on", "1", "true":
			return true
		}
		return false
	}
	if !RecordHydrationEnabled() || !enabled("FLOW_PROJECT_RECORD_MUTATIONS") && !enabled("FLOW_INITIATIVE_RECORD_MUTATIONS") {
		return a, b, protected
	}
	left, right := reflect.ValueOf(&a.Data).Elem(), reflect.ValueOf(&b.Data).Elem()
	for i := 0; i < left.NumField(); i++ {
		x, y := left.Field(i), right.Field(i)
		if x.CanSet() && y.CanSet() && sameMetadataValue(x, y) {
			// Shared output IDs still bind references in the unequal fields.
			// Preserve that binding before removing their defining collection.
			raw, err := json.Marshal(x.Interface())
			if err != nil {
				continue
			}
			for _, stamp := range mutationVerifyStamp.FindAll(raw, -1) {
				protected = append(append(protected, ' '), stamp...)
			}
			x.SetZero()
			y.SetZero()
		}
	}
	return a, b, protected
}
