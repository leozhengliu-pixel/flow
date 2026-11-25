package store

import (
	"context"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21MigrateOAuthIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	appID, clientID := idgen.New("p21-app-"), idgen.New("p21-client-")
	publicID, publicClient := idgen.New("p21-app-"), idgen.New("p21-client-")
	secret := "p21-fixture-secret"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.oauth_identity", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications,
			domain.OAuthApplication{ID: appID, ClientID: clientID, Name: "P21 confidential", ClientSecret: secret, Description: strings.Repeat("unrelated application body ", 30000)},
			domain.OAuthApplication{ID: publicID, ClientID: publicClient, Name: "P21 public"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "P21 other application workspace", idgen.New("p21-migrate-oauth-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
			t.Errorf("clean up owned application workspace: %v", err)
		}
	})
	otherApp, otherClient := idgen.New("p21-app-"), idgen.New("p21-client-")
	if err := f.MutateWorkspace(t.Context(), other.Workspace.URLKey, "scale_p21.oauth_other", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: otherApp, ClientID: otherClient, Name: "P21 other workspace application", ClientSecret: "p21-other-fixture-secret"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := probe.ReloadWorkspace(t.Context(), other.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	compare := func() {
		t.Helper()
		for _, client := range []string{clientID, publicClient, otherClient, strings.ToUpper(clientID), clientID + " ", "", "p21-client-missing"} {
			left, leftOK := f.OAuthApplicationHome(client)
			right, rightOK := probe.OAuthApplicationHome(client)
			if left != right || leftOK != rightOK {
				t.Fatal("application home differs from the legacy identity lookup")
			}
			if client == clientID {
				value, found, release := probe.residentOAuthIdentityValue("", client, residentOAuthConfidentialSecret)
				charged := probe.metadataResidency.work.used
				release()
				if found && charged < max(2<<20, 3*len(value)+(1<<20)) {
					t.Fatal("scalar processing lost its work lease or larger reservation")
				}
			}
			for _, credential := range []string{secret, "", "p21-wrong-secret", "p21-other-fixture-secret"} {
				leftConfidential, leftOK := f.OAuthClientSecret(client, credential)
				rightConfidential, rightOK := probe.OAuthClientSecret(client, credential)
				if leftConfidential != rightConfidential || leftOK != rightOK {
					t.Fatal("confidential-client authentication differs from legacy")
				}
			}
		}
		for _, key := range []string{workspace, other.Workspace.URLKey, "", "p21-workspace-missing"} {
			for _, app := range []string{appID, publicID, otherApp, strings.ToUpper(appID), appID + " ", "", "p21-app-missing"} {
				left, leftOK := f.OAuthApplicationSecret(key, app)
				right, rightOK := probe.OAuthApplicationSecret(key, app)
				if left != right || leftOK != rightOK {
					t.Fatal("application secret identity or workspace binding differs from legacy")
				}
			}
		}
	}
	compare()
	secret = "p21-rotated-fixture-secret"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.oauth_rotation", appID, nil, func(data *domain.Bootstrap) error {
		for i := range data.OAuthApplications {
			if data.OAuthApplications[i].ID == appID {
				data.OAuthApplications[i].ClientSecret = secret
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The native reader must see revocation/rotation without a peer wake.
	compare()
	if confidential, accepted := probe.OAuthClientSecret(clientID, "p21-fixture-secret"); !confidential || accepted {
		t.Fatal("rotated confidential secret remained valid")
	}
	for _, value := range []string{"null", strings.Repeat("\u4e2d", 200000)} {
		secret = value
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.oauth_scalar", appID, nil, func(data *domain.Bootstrap) error {
			for i := range data.OAuthApplications {
				if data.OAuthApplications[i].ID == appID {
					data.OAuthApplications[i].ClientSecret = secret
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		compare()
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.oauth_remove", appID, nil, func(data *domain.Bootstrap) error {
		for i := range data.OAuthApplications {
			if data.OAuthApplications[i].ID == appID {
				data.OAuthApplications = append(data.OAuthApplications[:i], data.OAuthApplications[i+1:]...)
				break
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	compare()
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("OAuth identity reader decoded a workspace or leaked admission")
	}
}
