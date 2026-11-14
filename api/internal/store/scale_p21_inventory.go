package store

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

// Each source file owns its registration file. Parallel migrations never edit
// a shared consumer list or a repository-wide expected count.
//
//go:embed scale_p21_inventory/*/*.json
var metadataConsumerFiles embed.FS

type metadataConsumerReview struct {
	Owner             string   `json:"owner"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	Accessors         []string `json:"accessors"`
	Temperature       string   `json:"temperature,omitempty"`
	TemperatureReason string   `json:"temperatureReason,omitempty"`
	Callers           []string `json:"callers,omitempty"`
}

type metadataConsumerFile struct {
	Source     string                            `json:"source"`
	Unmigrated int                               `json:"unmigrated"`
	Consumers  map[string]metadataConsumerReview `json:"consumers"`
}

type metadataConsumerInventory struct {
	Files     map[string]metadataConsumerFile
	Consumers map[string]metadataConsumerReview
}

var metadataConsumerReviews = sync.OnceValues(func() (metadataConsumerInventory, error) {
	return readMetadataConsumerInventory(metadataConsumerFiles)
})

func readMetadataConsumerInventory(files fs.FS) (metadataConsumerInventory, error) {
	result := metadataConsumerInventory{Files: map[string]metadataConsumerFile{}, Consumers: map[string]metadataConsumerReview{}}
	paths, err := fs.Glob(files, "scale_p21_inventory/*/*.json")
	if err != nil {
		return result, err
	}
	if len(paths) == 0 {
		return result, fmt.Errorf("metadata consumer inventory is empty")
	}
	for _, path := range paths {
		raw, err := fs.ReadFile(files, path)
		if err != nil {
			return result, err
		}
		var file metadataConsumerFile
		if err := json.Unmarshal(raw, &file); err != nil {
			return result, fmt.Errorf("%s: %w", path, err)
		}
		expected := strings.TrimSuffix(strings.TrimPrefix(path, "scale_p21_inventory/"), ".json") + ".go"
		if file.Source != expected || file.Unmigrated < 0 {
			return result, fmt.Errorf("%s: source or count does not match its file", path)
		}
		count := 0
		for name, review := range file.Consumers {
			if name == "" || strings.Contains(name, "/") {
				return result, fmt.Errorf("%s: invalid consumer name", path)
			}
			if review.Owner != "P16" && review.Owner != "P17" && review.Owner != "P18" && review.Owner != "P21" {
				return result, fmt.Errorf("%s: missing owner for %s", path, name)
			}
			switch review.Status {
			case "unmigrated":
				count++
			case "reviewed":
				if strings.TrimSpace(review.Reason) == "" {
					return result, fmt.Errorf("%s: missing review reason for %s", path, name)
				}
			default:
				return result, fmt.Errorf("%s: invalid review status for %s", path, name)
			}
			if len(review.Accessors) == 0 {
				return result, fmt.Errorf("%s: missing accessor classification for %s", path, name)
			}
			switch review.Temperature {
			case "":
				review.Temperature = "hot"
			case "hot":
			case "cold":
				if strings.TrimSpace(review.TemperatureReason) == "" {
					return result, fmt.Errorf("%s: missing cold reason for %s", path, name)
				}
			default:
				return result, fmt.Errorf("%s: invalid consumer temperature for %s", path, name)
			}
			file.Consumers[name] = review
			result.Consumers[file.Source+":"+name] = review
		}
		if count != file.Unmigrated {
			return result, fmt.Errorf("%s: unmigrated=%d, entries=%d", path, file.Unmigrated, count)
		}
		result.Files[file.Source] = file
	}
	return result, nil
}

func registeredMetadataConsumers() map[string]metadataFullWorkspaceConsumer {
	inventory, err := metadataConsumerReviews()
	if err != nil {
		// A malformed embedded registration must never disable the guard.
		return map[string]metadataFullWorkspaceConsumer{"inventory/invalid": {owner: "P21"}}
	}
	result := map[string]metadataFullWorkspaceConsumer{}
	for name, review := range inventory.Consumers {
		if review.Status == "unmigrated" {
			result[name] = metadataFullWorkspaceConsumer{owner: review.Owner}
		}
	}
	return result
}

func registeredHotMetadataConsumers() map[string]metadataFullWorkspaceConsumer {
	inventory, err := metadataConsumerReviews()
	if err != nil {
		return map[string]metadataFullWorkspaceConsumer{"inventory/invalid": {owner: "P21"}}
	}
	return hotMetadataConsumers(inventory)
}

func hotMetadataConsumers(inventory metadataConsumerInventory) map[string]metadataFullWorkspaceConsumer {
	result := map[string]metadataFullWorkspaceConsumer{}
	for name, review := range inventory.Consumers {
		cold := review.Temperature == "cold" && strings.TrimSpace(review.TemperatureReason) != ""
		if review.Status == "unmigrated" && !cold {
			result[name] = metadataFullWorkspaceConsumer{owner: review.Owner}
		}
	}
	return result
}

func registeredMetadataReviews() map[string]string {
	inventory, err := metadataConsumerReviews()
	if err != nil {
		return nil
	}
	result := map[string]string{}
	for name, review := range inventory.Consumers {
		if review.Status == "reviewed" {
			result[name] = review.Reason
		}
	}
	return result
}
