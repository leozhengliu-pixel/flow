package main

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"testing"
	"time"

	"flow/api/internal/store"
)

func scaleP21SW19JSON(t *testing.T, value any, clocks ...time.Time) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var replace func(any) any
	replace = func(value any) any {
		switch item := value.(type) {
		case string:
			for _, clock := range clocks {
				if !clock.IsZero() && item == clock.Format(time.RFC3339Nano) {
					return "<generated-clock>"
				}
			}
		case map[string]any:
			for key, value := range item {
				item[key] = replace(value)
			}
		case []any:
			for i, value := range item {
				item[i] = replace(value)
			}
		}
		return value
	}
	return replace(decoded)
}

func scaleP21SW19Hash(t *testing.T, repo *store.SQLiteStore, workspace, kind, exclude string) [32]byte {
	t.Helper()
	hash := sha256.New()
	if err := repo.StreamMetadataRecords(t.Context(), workspace, kind, func(key store.RecordKey, reader io.Reader) error {
		if key.ID == exclude {
			return nil
		}
		if _, err := io.WriteString(hash, key.ID+"\x00"); err != nil {
			return err
		}
		_, err := io.Copy(hash, reader)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}
