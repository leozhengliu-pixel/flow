package main

import (
	"os"
	"testing"
)

// Opt-in measurement includes authentication, workspace authorization and the
// public detail handler, with a constant small target and unrelated body growth.
func TestScaleRequestAllocations(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P03_MEASURE") != "1" {
		t.Skip("set FLOW_SCALE_P03_MEASURE=1 for the allocation measurement")
	}
	scaleMeasureRequestCosts(t)
}
