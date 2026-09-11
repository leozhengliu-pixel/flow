//go:build integration

package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestExternalAPIKeyAuthenticationAndUsage(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		t.Skip("isolated external database required")
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := domain.APIKey{ID: "external-auth-key", SecretHash: strings.Repeat("a", 64), CreatorID: data.Viewer.ID, Scopes: []string{"read"}}
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	other, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil); err != nil {
		t.Fatal("second instance cannot see live credential", err)
	}
	if err := repo.RecordAPIKeyUse(ctx, data.Workspace.URLKey, key.ID, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		result, err := other.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Key.LastUsedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("usage not persisted across instances")
		}
		time.Sleep(10 * time.Millisecond)
	}
	now := time.Now().UTC()
	key.RevokedAt = &now
	insertSearchMetadata(t, repo, data.Workspace.URLKey, "apiKeys", key.ID, key)
	if _, err := other.AuthenticateAPIKeyRecord(ctx, "", key.SecretHash, nil); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("other instance retained revoked key", err)
	}
}
