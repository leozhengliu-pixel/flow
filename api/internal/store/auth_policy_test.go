package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW2AuthenticationProviderPolicy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	providers := make([]domain.IdentityProvider, 75)
	for i := range providers {
		providers[i] = domain.IdentityProvider{ID: fmt.Sprintf("sw2-provider-%d", i), Enabled: true, Enforced: true, Issuer: fmt.Sprintf("https://idp.example.test/%d///", i)}
	}
	providers[0].Name = strings.Repeat("large ignored provider name ", 15000)
	providers[1].Enabled = false
	providers[2].Enforced = false
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw2.seed", "", nil, func(data *domain.Bootstrap) error {
		data.IdentityProviders = providers
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, auth := range []SessionAuthentication{
		{Provider: "enterprise:sw2-provider-74"},
		{Provider: "enterprise:SW2-provider-74"},
		{Provider: "enterprise:sw2-provider-1"},
		{Provider: "enterprise:sw2-provider-2"},
		{Provider: "oidc", Issuer: "https://idp.example.test/74"},
		{Provider: "saml", Issuer: "https://idp.example.test/74////"},
		{Provider: "oidc", Issuer: "https://IDP.example.test/74"},
		{Provider: "oidc", Issuer: "https://idp.example.test/1"},
		{Provider: "password"},
	} {
		t.Run(auth.Provider+auth.Issuer, func(t *testing.T) {
			enforced, matched := false, false
			for _, provider := range providers {
				if provider.Enabled && provider.Enforced {
					enforced = true
					if auth.Provider == "enterprise:"+provider.ID || (auth.Provider == "oidc" || auth.Provider == "saml") && strings.TrimRight(auth.Issuer, "/") == strings.TrimRight(provider.Issuer, "/") {
						matched = true
					}
				}
			}
			gotEnforced, gotMatched, err := f.AuthenticationProviderPolicy(t.Context(), workspace, auth)
			if err != nil || gotEnforced != enforced || gotMatched != matched {
				t.Fatalf("policy=%v/%v err=%v; want %v/%v", gotEnforced, gotMatched, err, enforced, matched)
			}
		})
	}
	if _, _, err := f.AuthenticationProviderPolicy(t.Context(), workspace+"-missing", SessionAuthentication{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing workspace error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := f.AuthenticationProviderPolicy(ctx, workspace, SessionAuthentication{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw2.clear", "", nil, func(data *domain.Bootstrap) error {
		data.IdentityProviders = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if enforced, matched, err := f.AuthenticationProviderPolicy(t.Context(), workspace, SessionAuthentication{Provider: "password"}); err != nil || enforced || matched {
		t.Fatalf("empty policy=%v/%v err=%v", enforced, matched, err)
	}
}
