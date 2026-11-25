package store

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestScaleP21InventoryPartitionTotals(t *testing.T) {
	inventory, err := metadataConsumerReviews()
	if err != nil {
		t.Fatal(err)
	}
	total, pending, reviewed := 0, 0, 0
	accessors := map[string]int{}
	for _, file := range inventory.Files {
		pending += file.Unmigrated
		total += len(file.Consumers)
		for _, consumer := range file.Consumers {
			if consumer.Status == "reviewed" {
				reviewed++
				continue
			}
			for _, accessor := range consumer.Accessors {
				accessors[accessor]++
			}
		}
	}
	if total != len(inventory.Consumers) || pending != len(metadataFullWorkspaceConsumers()) || reviewed != len(registeredMetadataReviews()) || total != pending+reviewed {
		t.Fatalf("partition totals differ: total=%d pending=%d reviewed=%d", total, pending, reviewed)
	}
	t.Logf("source files=%d total=%d unmigrated=%d reviewed=%d", len(inventory.Files), total, pending, reviewed)
	for _, name := range strings.Fields("MutateWorkspace workspaceData WorkspaceMetadata MutateWorkspaceWithAggregate") {
		t.Logf("remaining %s=%d", name, accessors[name])
	}
}

func TestScaleP21InventoryPartitionRejectsInvalidReviews(t *testing.T) {
	base := metadataConsumerFile{Source: "server/example.go", Unmigrated: 1, Consumers: map[string]metadataConsumerReview{"read": {Owner: "P21", Status: "unmigrated", Accessors: []string{"workspaceData"}}}}
	for _, test := range []struct {
		name string
		edit func(*metadataConsumerFile)
	}{
		{"source", func(file *metadataConsumerFile) { file.Source = "server/other.go" }},
		{"count", func(file *metadataConsumerFile) { file.Unmigrated = 0 }},
		{"reason", func(file *metadataConsumerFile) {
			file.Unmigrated = 0
			review := file.Consumers["read"]
			review.Status = "reviewed"
			file.Consumers["read"] = review
		}},
		{"status", func(file *metadataConsumerFile) {
			review := file.Consumers["read"]
			review.Status = "unknown"
			file.Consumers["read"] = review
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, _ := json.Marshal(base)
			var file metadataConsumerFile
			if err := json.Unmarshal(encoded, &file); err != nil {
				t.Fatal(err)
			}
			test.edit(&file)
			encoded, _ = json.Marshal(file)
			files := fstest.MapFS{"scale_p21_inventory/server/example.json": {Data: encoded}}
			if _, err := readMetadataConsumerInventory(files); err == nil {
				t.Fatal("invalid partition was accepted")
			}
		})
	}
}
