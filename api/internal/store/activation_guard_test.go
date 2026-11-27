package store

import (
	"errors"
	"testing"
)

// Activation accepts the reviewed inventory once no HOT consumer is left:
// unmigrated COLD consumers run on the COLD facade. The other activation
// requirements still refuse.
func TestScaleActivationGuardAcceptsColdConsumers(t *testing.T) {
	if hot := registeredHotMetadataConsumers(); len(hot) != 0 {
		t.Fatalf("HOT consumers left: %v", hot)
	}
	if len(metadataFullWorkspaceConsumers()) == 0 {
		t.Log("no COLD consumers left either")
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	if err := validateBoundedMetadataCoverage("sqlite"); err != nil {
		t.Fatalf("activation refused: %v", err)
	}
	t.Setenv("FLOW_RECORD_PEER_SYNC", "shadow")
	if err := validateBoundedMetadataCoverage("sqlite"); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("peer shadow accepted: %v", err)
	}
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	if err := validateBoundedMetadataCoverage("sqlite"); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("journal off accepted: %v", err)
	}
}

// An unmigrated consumer without a COLD review is HOT and refuses activation.
func TestScaleActivationGuardRefusesUnreviewedCold(t *testing.T) {
	inventory := metadataConsumerInventory{Consumers: map[string]metadataConsumerReview{
		"a.go:hot":          {Owner: "P21", Status: "unmigrated"},
		"a.go:cold-no-why":  {Owner: "P21", Status: "unmigrated", Temperature: "cold"},
		"a.go:cold":         {Owner: "P21", Status: "unmigrated", Temperature: "cold", TemperatureReason: "settings page"},
		"a.go:reviewed-hot": {Owner: "P21", Status: "reviewed"},
	}}
	hot := hotMetadataConsumers(inventory)
	if len(hot) != 2 {
		t.Fatalf("hot=%v", hot)
	}
	for _, name := range []string{"a.go:hot", "a.go:cold-no-why"} {
		if _, found := hot[name]; !found {
			t.Fatalf("%s not HOT: %v", name, hot)
		}
	}
}
