package store

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestMetadataRecordDecodeOmitsCredentialData(t *testing.T) {
	const key = "synthetic-record-key"
	for _, collection := range []string{"apiKeys", "passkeys", "passkeyRegistrationChallenges", "oauthAuthorizations", "agentSessions"} {
		t.Run(collection, func(t *testing.T) {
			field, kind, ok := metadataRecordType(collection)
			if !ok {
				t.Fatal("expected a record-backed collection")
			}
			_, err := mergeWithStoredRecord(issueReferences{}, field, kind, key, []byte(`{}`), []byte(`{}`), []byte(`1`))
			var cause *json.UnmarshalTypeError
			if !errors.As(err, &cause) || !errors.Is(err, cause) {
				t.Fatal("the typed decode cause was lost")
			}
			if message := err.Error(); !strings.Contains(message, collection) || strings.Contains(message, key) {
				t.Fatal("expected an error message naming only the collection")
			}

			const value = "synthetic-invalid-timestamp"
			_, err = mergeWithStoredRecord(issueReferences{}, field, kind, key, []byte(`{}`), []byte(`{}`), []byte(`{"createdAt":"`+value+`"}`))
			var parseCause *time.ParseError
			if !errors.As(err, &parseCause) || parseCause.Value != value {
				t.Fatal("expected a decode cause containing the invalid value")
			}
			if message := err.Error(); !strings.Contains(message, collection) || strings.Contains(message, key) || strings.Contains(message, value) {
				t.Fatal("the error message exposed record data or omitted the collection")
			}
		})
	}
}

func TestBoundedMetadataSize(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int
		parts []int
		want  int
	}{
		{"exact", 32, []int{0, 24, 8}, 32},
		{"one over", 32, []int{24, 9}, -1},
		{"max int", math.MaxInt, []int{math.MaxInt - 1, 1}, math.MaxInt},
		{"overflow", math.MaxInt, []int{math.MaxInt, 1}, -1},
		{"negative part", 32, []int{24, -1}, -1},
		{"negative limit", -1, nil, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			size, err := boundedMetadataSize(test.limit, test.parts...)
			if test.want < 0 {
				if size != 0 || !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatalf("got size=%d err=%v, want work budget error", size, err)
				}
			} else if err != nil || size != test.want {
				t.Fatalf("got size=%d err=%v, want size=%d", size, err, test.want)
			}
		})
	}
}

func TestMetadataSourceBudgetRejectsOverflow(t *testing.T) {
	t.Setenv("FLOW_METADATA_SOURCE_BYTES", "33554432")
	for _, count := range []int64{-1, math.MaxInt64, (math.MaxInt64-metadataSourceReloadHeadroom)/metadataLogicalDescriptorBytes + 1} {
		if err := checkMetadataSourceBudget(count); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatalf("count=%d: got %v, want work budget error", count, err)
		}
	}
}
