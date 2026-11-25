package store

import (
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP16EqualCollectionsKeepGeneratedReferenceBindings(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	x, y, z := "p_1234567890123456789_abcdefghij", "p_2234567890123456789_abcdefghij", "p_3234567890123456789_abcdefghij"
	result := func(reference string) mutationCallbackResult {
		return mutationCallbackResult{Data: domain.Bootstrap{Projects: []domain.Project{{ID: x}}, Notifications: []domain.Notification{{SourceID: reference}}}}
	}
	for _, test := range []struct {
		name, left, right string
		different         bool
	}{{"same-generated-reference", x, x, false}, {"different-alias", x, y, true}, {"independent-generated-ids", y, z, false}} {
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range []string{"off", "on"} {
				t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
				if got := compareMutationCallbacks(result(test.left), result(test.right), nil) != ""; got != test.different {
					t.Fatalf("%s: alias difference=%t, want %t", mode, got, test.different)
				}
			}
		})
	}
}
