package main

import (
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleCredentialRevocationDifferential(t *testing.T) {
	var operations []pointDifferentialOperation
	for _, owned := range []bool{true, false} {
		name := "other-key"
		if owned {
			name = "own-key"
		}
		operations = append(operations, pointDifferentialOperation{
			name: name, kind: "apiKeys", id: "m3-target-key", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				owner := "m3-other-owner"
				if owned {
					owner = actor.ID
				}
				data.APIKeys = []domain.APIKey{
					{ID: "m3-target-key", CreatorID: owner, Name: "Target", Prefix: "m3-prefix", SecretHash: "m3-test-hash", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
					{ID: "m3-other-key", CreatorID: "m3-other-owner", Name: "Untouched", SecretHash: "m3-other-test-hash", Scopes: []string{"read", "write"}, TeamRestriction: "all"},
				}
			},
			handler: (*server).revokeAPIKey,
		})
	}
	checkPointOperationDifferential(t, operations)
}

func TestScaleNotificationPreferencesDifferential(t *testing.T) {
	checkPointOperationDifferential(t, []pointDifferentialOperation{
		{
			name: "notification-preferences", kind: "notificationPreferences", id: "m3-preferences", body: `{"emailFormat":"immediate","inbox":{"enabled":true,"categories":{"pulse":false}}}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				preferences := defaultPreferences(actor.ID)
				// The generic fixture's sentinel is also a different user's
				// preference record that the point write must retain.
				data.NotificationPreferences = map[string]domain.NotificationPreferences{
					actor.ID: preferences, "m3-preferences": {UserID: "m3-preferences", EmailFormat: "digest"},
				}
			},
			handler: (*server).updateNotificationPreferences,
			readKeys: func(actor domain.User) []store.RecordKey {
				return []store.RecordKey{{Kind: "notificationPreferences", ID: actor.ID}, {Kind: "notificationPreferences", ID: "m3-preferences"}}
			},
		},
	})
}

func TestScalePushSubscriptionDifferential(t *testing.T) {
	var operations []pointDifferentialOperation
	for _, owned := range []bool{true, false} {
		name := "other-push-subscription"
		if owned {
			name = "own-push-subscription"
		}
		operations = append(operations, pointDifferentialOperation{
			name: name, kind: "pushSubscriptions", id: "m3-push", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				owner := "m3-other-owner"
				if owned {
					owner = actor.ID
				}
				data.PushSubscriptions = []domain.PushSubscription{
					{ID: "m3-push", UserID: owner, Endpoint: "https://push.example.test/target", P256DH: "fixture-public-key", Auth: "fixture-auth", Enabled: true},
					{ID: "m3-other-push", UserID: actor.ID, Endpoint: "https://push.example.test/other", P256DH: "retained-key", Auth: "retained-auth", Enabled: true},
				}
			},
			handler:   (*server).deletePushSubscription,
			otherKeys: []store.RecordKey{{Kind: "pushSubscriptions", ID: "m3-other-push"}},
		})
	}
	checkPointOperationDifferential(t, operations)
}
