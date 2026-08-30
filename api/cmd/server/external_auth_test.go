package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/store"
)

func TestAuthProvidersAndEmailGate(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := &server{store: repository, uploadPath: t.TempDir(), externalAuth: &externalAuth{config: config.AuthConfig{EmailEnabled: false, SAML: config.SAMLProvider{Enabled: true, DisplayName: "Company SSO"}}, providers: map[string]*oidcClient{"oidc": {name: "Enterprise OIDC"}}, saml: nil}}
	handler := newHandler(server)
	request := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("providers status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Email     bool                        `json:"email"`
		Providers []struct{ ID, Name string } `json:"providers"`
	}
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Email || len(result.Providers) != 1 || result.Providers[0].ID != "oidc" {
		t.Fatalf("providers response=%#v", result)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled email login status=%d", response.Code)
	}
}

func TestAllowedExternalEmail(t *testing.T) {
	if !allowedExternalEmail("User@Example.com", []string{"example.com"}) || allowedExternalEmail("user@other.com", []string{"example.com"}) {
		t.Fatal("external email domain restriction failed")
	}
}

func TestConfigureSAMLFromMetadata(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificateRaw, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Flow SAML test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Flow SAML test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateRaw})
	metadata := `<?xml version="1.0"?><EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example"><IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><KeyDescriptor use="signing"><KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>` + base64.StdEncoding.EncodeToString(certificateRaw) + `</X509Certificate></X509Data></KeyInfo></KeyDescriptor><SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example/sso"/></IDPSSODescriptor></EntityDescriptor>`
	middleware, err := configureSAML(t.Context(), config.SAMLProvider{Enabled: true, MetadataXML: metadata, EntityID: "https://flow.example", ACSURL: "https://flow.example/api/auth/saml/acs", SPPrivateKey: string(keyPEM), SPCertificate: string(certPEM)}, "https://flow.example")
	if err != nil {
		t.Fatal(err)
	}
	if middleware.ServiceProvider.AcsURL.Path != "/api/auth/saml/acs" || middleware.ServiceProvider.MetadataURL.Path != "/api/auth/saml/metadata" {
		t.Fatalf("SAML endpoints: ACS=%s metadata=%s", middleware.ServiceProvider.AcsURL.Path, middleware.ServiceProvider.MetadataURL.Path)
	}
}
