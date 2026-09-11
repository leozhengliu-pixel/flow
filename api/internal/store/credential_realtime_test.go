package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestRoutineOAuthActivityRetainsAuditWithoutRealtimeBroadcast(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "credentials.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	realtime, webhooks := []string{}, []string{}
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event.Type) })
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhooks = append(webhooks, event.Type) })
	grant := domain.OAuthAuthorizationCode{ClientID: "client", WorkspaceKey: data.Workspace.URLKey, UserID: data.Viewer.ID, Scopes: []string{"read"}, ExpiresAt: time.Now().Add(time.Hour)}
	created, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "first-code", grant, domain.OAuthAuthorization{ID: "consent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "second-code", grant, domain.OAuthAuthorization{ID: "proposed-second-consent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.AuthorizationID != reused.AuthorizationID {
		t.Fatal("test did not reuse consent")
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "authorization_code", "second-code", grant.ClientID, "refresh", domain.APIKey{ID: "access", SecretHash: "secret-hash"}, func(domain.OAuthAuthorizationCode) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(realtime, []string{"oauth_authorization.created"}) {
		t.Fatalf("routine credentials broadcast workspace changes: %v", realtime)
	}
	if !slices.Equal(webhooks, []string{"oauth_authorization.created", "oauth_authorization.reused", "oauth_token.created"}) {
		t.Fatalf("credential webhook audit lost: %v", webhooks)
	}
	for _, kind := range webhooks {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE event_type=?`, kind).Scan(&count); err != nil || count != 1 {
			t.Fatalf("durable audit missing %s: %d %v", kind, count, err)
		}
	}
	if err := repo.RevokeOAuthAuthorizationRecords(t.Context(), grant.WorkspaceKey, created.AuthorizationID, data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if realtime[len(realtime)-1] != "oauth_authorization.revoked" {
		t.Fatal("real revocation stopped reaching browsers")
	}
}
