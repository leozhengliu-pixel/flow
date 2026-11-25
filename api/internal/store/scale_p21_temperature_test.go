package store

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func scaleP21TemperatureFiles(t *testing.T, files ...metadataConsumerFile) fstest.MapFS {
	t.Helper()
	result := fstest.MapFS{}
	for _, file := range files {
		raw, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		name := "scale_p21_inventory/" + strings.TrimSuffix(file.Source, ".go") + ".json"
		result[name] = &fstest.MapFile{Data: raw}
	}
	return result
}

func TestScaleP21TemperatureMissingDefaultsHot(t *testing.T) {
	review := metadataConsumerReview{Owner: "P21", Status: "unmigrated", Accessors: []string{"WorkspaceMetadata"}}
	files := scaleP21TemperatureFiles(t, metadataConsumerFile{
		Source: "store/example.go", Unmigrated: 1,
		Consumers: map[string]metadataConsumerReview{"read": review},
	})
	raw := string(files["scale_p21_inventory/store/example.json"].Data)
	if strings.Contains(raw, `"temperature"`) {
		t.Fatal("fixture does not exercise missing temperature")
	}
	inventory, err := readMetadataConsumerInventory(files)
	if err != nil {
		t.Fatal(err)
	}
	key := "store/example.go:read"
	if inventory.Consumers[key].Temperature != "hot" || inventory.Files["store/example.go"].Consumers["read"].Temperature != "hot" {
		t.Fatal("missing temperature did not default hot in both inventory views")
	}
	if _, found := hotMetadataConsumers(inventory)[key]; !found {
		t.Fatal("unclassified pending consumer escaped the hot guard")
	}
}

func TestScaleP21TemperatureRejectsMalformed(t *testing.T) {
	base := metadataConsumerFile{
		Source: "store/example.go", Unmigrated: 1,
		Consumers: map[string]metadataConsumerReview{"read": {
			Owner: "P21", Status: "unmigrated", Accessors: []string{"WorkspaceMetadata"},
			Temperature: "cold", TemperatureReason: "Rare administrative route.", Callers: []string{"POST /api/admin/example"},
		}},
	}
	for _, test := range []struct {
		name        string
		temperature string
		reason      string
	}{
		{"unknown", "warm", "Unknown class."},
		{"uppercase", "COLD", "Wrong enum."},
		{"spaced-enum", " cold", "Wrong enum."},
		{"missing-cold-reason", "cold", ""},
		{"blank-cold-reason", "cold", " \n\t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := base
			review := base.Consumers["read"]
			review.Temperature, review.TemperatureReason = test.temperature, test.reason
			file.Consumers = map[string]metadataConsumerReview{"read": review}
			if _, err := readMetadataConsumerInventory(scaleP21TemperatureFiles(t, file)); err == nil {
				t.Fatal("invalid temperature registration was accepted")
			}
		})
	}
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"invalid-json", `{"source":`},
		{"nonstring-temperature", `{"source":"store/example.go","unmigrated":1,"consumers":{"read":{"owner":"P21","status":"unmigrated","accessors":["WorkspaceMetadata"],"temperature":1}}}`},
		{"nonarray-callers", `{"source":"store/example.go","unmigrated":1,"consumers":{"read":{"owner":"P21","status":"unmigrated","accessors":["WorkspaceMetadata"],"callers":"route"}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := fstest.MapFS{"scale_p21_inventory/store/example.json": {Data: []byte(test.raw)}}
			if _, err := readMetadataConsumerInventory(files); err == nil {
				t.Fatal("malformed temperature registration was accepted")
			}
		})
	}
}

func TestScaleP21TemperaturePartitionsPending(t *testing.T) {
	pending := func(temperature string) metadataConsumerReview {
		review := metadataConsumerReview{
			Owner: "P21", Status: "unmigrated", Accessors: []string{"WorkspaceMetadata"},
			Temperature: temperature,
		}
		if temperature == "cold" {
			review.TemperatureReason = "Rare setup operation."
			review.Callers = []string{"POST /api/example/setup"}
		}
		return review
	}
	reviewedHot := pending("hot")
	reviewedHot.Status, reviewedHot.Reason = "reviewed", "Bounded hot reader verified."
	reviewedCold := pending("cold")
	reviewedCold.Status, reviewedCold.Reason = "reviewed", "Original cold operation retained."
	files := scaleP21TemperatureFiles(t,
		metadataConsumerFile{Source: "server/example.go", Unmigrated: 3, Consumers: map[string]metadataConsumerReview{
			"unknown": pending(""), "hot": pending("hot"), "cold": pending("cold"), "reviewed": reviewedHot,
		}},
		metadataConsumerFile{Source: "store/example.go", Unmigrated: 2, Consumers: map[string]metadataConsumerReview{
			"hot": pending("hot"), "cold": pending("cold"), "reviewed": reviewedCold,
		}},
	)
	inventory, err := readMetadataConsumerInventory(files)
	if err != nil {
		t.Fatal(err)
	}
	hot := hotMetadataConsumers(inventory)
	if len(inventory.Consumers) != 7 || len(hot) != 3 {
		t.Fatalf("partition differs: total=%d hot pending=%d", len(inventory.Consumers), len(hot))
	}
	for _, key := range []string{"server/example.go:unknown", "server/example.go:hot", "store/example.go:hot"} {
		if consumer, found := hot[key]; !found || consumer.owner != "P21" {
			t.Fatalf("missing hot blocker %s", key)
		}
	}
	pendingTotal := 0
	for _, file := range inventory.Files {
		pendingTotal += file.Unmigrated
	}
	if pendingTotal != 5 {
		t.Fatalf("cold classification changed all-pending count: %d", pendingTotal)
	}
	cold := inventory.Consumers["server/example.go:cold"]
	if cold.TemperatureReason != "Rare setup operation." || !slices.Equal(cold.Callers, []string{"POST /api/example/setup"}) {
		t.Fatal("classification evidence was lost")
	}
	// A directly constructed inventory must also fail closed for missing data.
	inventory.Consumers["store/example.go:hot"] = pending("")
	if len(hotMetadataConsumers(inventory)) != 3 {
		t.Fatal("empty classification escaped the hot guard")
	}
	invalidCold := pending("cold")
	invalidCold.TemperatureReason = ""
	inventory.Consumers["store/example.go:hot"] = invalidCold
	if len(hotMetadataConsumers(inventory)) != 3 {
		t.Fatal("unexplained cold classification escaped the hot guard")
	}
}
