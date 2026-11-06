package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/store"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/crewjam/saml/xmlenc"
	dsig "github.com/russellhaering/goxmldsig"
)

const (
	samlTestIdP = "https://idp.example"
	samlTestSP  = "https://flow.example"
	samlTestACS = "https://flow.example/api/auth/saml/acs"
)

type samlTestFixture struct {
	middleware *samlsp.Middleware
	idpKey     *rsa.PrivateKey
	idpCert    []byte
	handler    http.Handler
}

func newSAMLTestKey(t *testing.T, name string) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, certificate
}

func newSAMLTestFixture(t *testing.T) samlTestFixture {
	t.Helper()
	idpKey, idpCert := newSAMLTestKey(t, "Flow SAML test IdP")
	spKey, spCert := newSAMLTestKey(t, "Flow SAML test SP")
	metadata := `<?xml version="1.0"?><EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="` + samlTestIdP + `"><IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><KeyDescriptor use="signing"><KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>` + base64.StdEncoding.EncodeToString(idpCert) + `</X509Certificate></X509Data></KeyInfo></KeyDescriptor><SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example/sso"/></IDPSSODescriptor></EntityDescriptor>`
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(spKey)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: spCert})
	middleware, err := configureSAML(t.Context(), config.SAMLProvider{Enabled: true, MetadataXML: metadata, EntityID: samlTestSP, ACSURL: samlTestACS, SPPrivateKey: string(keyPEM), SPCertificate: string(certPEM)}, samlTestSP)
	if err != nil {
		t.Fatal(err)
	}
	// The crafted responses are IdP-initiated: there is no tracked request
	// cookie to answer.
	middleware.ServiceProvider.AllowIDPInitiated = true
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := &server{store: repository, uploadPath: t.TempDir(), externalAuth: &externalAuth{config: config.AuthConfig{SAML: config.SAMLProvider{Enabled: true, EntityID: samlTestSP}}, appURL: samlTestSP, providers: map[string]*oidcClient{}, saml: middleware}}
	return samlTestFixture{middleware: middleware, idpKey: idpKey, idpCert: idpCert, handler: newHandler(server)}
}

type samlTestAssertion struct {
	issuer     string
	subject    string // inner XML of saml:Subject before the confirmation; "-" drops the Subject
	conditions bool
	attribute  string
	namespaces string
	// confirmation replaces the bearer SubjectConfirmation when set.
	confirmation string
}

func validSAMLTestAssertion() samlTestAssertion {
	return samlTestAssertion{issuer: samlTestIdP, subject: `<saml:NameID Format="urn:oasis:names:tc:SAML:2.0:nameid-format:transient">_transient-1</saml:NameID>`, conditions: true, attribute: `<saml:Attribute Name="email"><saml:AttributeValue>ada@example.com</saml:AttributeValue></saml:Attribute>`}
}

func (a samlTestAssertion) xml() string {
	now := time.Now().UTC()
	instant := func(offset time.Duration) string { return now.Add(offset).Format(time.RFC3339) }
	var builder strings.Builder
	fmt.Fprintf(&builder, `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" %s ID="_assertion-1" Version="2.0" IssueInstant="%s">`, a.namespaces, instant(0))
	fmt.Fprintf(&builder, `<saml:Issuer>%s</saml:Issuer>`, a.issuer)
	if a.subject != "-" {
		confirmation := a.confirmation
		if confirmation == "" {
			confirmation = fmt.Sprintf(`<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData NotOnOrAfter="%s" Recipient="%s"/></saml:SubjectConfirmation>`, instant(5*time.Minute), samlTestACS)
		}
		fmt.Fprintf(&builder, `<saml:Subject>%s%s</saml:Subject>`, a.subject, confirmation)
	}
	if a.conditions {
		fmt.Fprintf(&builder, `<saml:Conditions NotBefore="%s" NotOnOrAfter="%s"><saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction></saml:Conditions>`, instant(-time.Minute), instant(5*time.Minute), samlTestSP)
	}
	fmt.Fprintf(&builder, `<saml:AuthnStatement AuthnInstant="%s" SessionIndex="_session-1"><saml:AuthnContext><saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:Password</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>`, instant(0))
	fmt.Fprintf(&builder, `<saml:AttributeStatement>%s</saml:AttributeStatement></saml:Assertion>`, a.attribute)
	return builder.String()
}

// signedAssertion signs the assertion with the IdP key, as a real IdP would:
// every case below carries a valid signature, so only Flow's checks stand
// between it and a session.
func (f samlTestFixture) signedAssertion(t *testing.T, assertion samlTestAssertion) *etree.Element {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromString(assertion.xml()); err != nil {
		t.Fatal(err)
	}
	context := dsig.NewDefaultSigningContext(dsig.TLSCertKeyStore(tls.Certificate{Certificate: [][]byte{f.idpCert}, PrivateKey: f.idpKey}))
	context.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	signed, err := context.SignEnveloped(doc.Root())
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func (f samlTestFixture) encryptedAssertion(t *testing.T, assertion samlTestAssertion) *etree.Element {
	t.Helper()
	doc := etree.NewDocument()
	doc.SetRoot(f.signedAssertion(t, assertion))
	plaintext, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	encryptor := xmlenc.OAEP()
	encryptor.BlockCipher = xmlenc.AES128CBC
	encryptor.DigestMethod = &xmlenc.SHA1
	data, err := encryptor.Encrypt(f.middleware.ServiceProvider.Certificate, plaintext, nil)
	if err != nil {
		t.Fatal(err)
	}
	data.CreateAttr("Type", "http://www.w3.org/2001/04/xmlenc#Element")
	encrypted := etree.NewElement("saml:EncryptedAssertion")
	encrypted.AddChild(data)
	return encrypted
}

func samlTestResponse(t *testing.T, assertion *etree.Element) string {
	t.Helper()
	doc := etree.NewDocument()
	response := doc.CreateElement("samlp:Response")
	response.CreateAttr("xmlns:samlp", samlProtocolNamespace)
	response.CreateAttr("xmlns:saml", samlAssertionNamespace)
	response.CreateAttr("ID", "_response-1")
	response.CreateAttr("Version", "2.0")
	response.CreateAttr("IssueInstant", time.Now().UTC().Format(time.RFC3339))
	response.CreateAttr("Destination", samlTestACS)
	response.CreateElement("saml:Issuer").SetText(samlTestIdP)
	response.CreateElement("samlp:Status").CreateElement("samlp:StatusCode").CreateAttr("Value", saml.StatusSuccess)
	response.AddChild(assertion)
	text, err := doc.WriteToString()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString([]byte(text))
}

func samlACSRequest(form url.Values) *http.Request {
	request := httptest.NewRequest(http.MethodPost, samlTestACS, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func samlSessionCookie(response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "token" && cookie.Value != "" {
			return true
		}
	}
	return false
}

func TestSAMLACSAcceptsValidSignedResponse(t *testing.T) {
	fixture := newSAMLTestFixture(t)
	for name, assertion := range map[string]*etree.Element{
		"plain":     fixture.signedAssertion(t, validSAMLTestAssertion()),
		"encrypted": fixture.encryptedAssertion(t, validSAMLTestAssertion()),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, samlACSRequest(url.Values{"SAMLResponse": {samlTestResponse(t, assertion)}}))
			if response.Code != http.StatusFound || !samlSessionCookie(response) {
				t.Fatalf("valid response: status=%d cookies=%v body=%s", response.Code, response.Result().Cookies(), response.Body.String())
			}
		})
	}
}

func TestSAMLACSRefusesMalformedSignedAssertions(t *testing.T) {
	fixture := newSAMLTestFixture(t)
	foreign := `xmlns:other="urn:example:other"`
	cases := map[string]struct {
		assertion samlTestAssertion
		encrypt   bool
		// upstream is what the library alone does with the same response:
		// "panic" or "session" (it would sign the user in).
		upstream string
	}{
		"missing Subject":            {assertion: func() samlTestAssertion { a := validSAMLTestAssertion(); a.subject = "-"; return a }(), upstream: "panic"},
		"missing Conditions":         {assertion: func() samlTestAssertion { a := validSAMLTestAssertion(); a.conditions = false; return a }(), upstream: "panic"},
		"missing Subject, encrypted": {assertion: func() samlTestAssertion { a := validSAMLTestAssertion(); a.subject = "-"; return a }(), encrypt: true, upstream: "panic"},
		"Subject without NameID":     {assertion: func() samlTestAssertion { a := validSAMLTestAssertion(); a.subject = ""; return a }(), upstream: "session"},
		"empty NameID": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.subject = `<saml:NameID> </saml:NameID>`
			return a
		}(), upstream: "session"},
		"foreign-namespace NameID only": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.namespaces = foreign
			a.subject = `<other:NameID>admin@example.com</other:NameID>`
			return a
		}(), upstream: "session"},
		"foreign NameID after SAML NameID": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.namespaces = foreign
			a.subject = `<saml:NameID>ada@example.com</saml:NameID><other:NameID>admin@example.com</other:NameID>`
			return a
		}(), upstream: "session"},
		"foreign NameID, encrypted": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.namespaces = foreign
			a.subject = `<other:NameID>admin@example.com</other:NameID>`
			return a
		}(), encrypt: true, upstream: "session"},
		"two SAML NameIDs": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.subject = `<saml:NameID>ada@example.com</saml:NameID><saml:NameID>admin@example.com</saml:NameID>`
			return a
		}(), upstream: "session"},
		"entity NameID format": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.subject = `<saml:NameID Format="urn:oasis:names:tc:SAML:2.0:nameid-format:entity">https://idp.example</saml:NameID>`
			return a
		}(), upstream: "session"},
		"unknown NameID format": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.subject = `<saml:NameID Format="urn:example:custom">ada</saml:NameID>`
			return a
		}(), upstream: "session"},
		"foreign-namespace email Attribute": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.namespaces = foreign
			a.attribute = `<other:Attribute Name="email"><saml:AttributeValue>admin@example.com</saml:AttributeValue></other:Attribute>`
			return a
		}(), upstream: "session"},
		"foreign-namespace AttributeValue": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.namespaces = foreign
			a.attribute = `<saml:Attribute Name="email"><other:AttributeValue>admin@example.com</other:AttributeValue></saml:Attribute>`
			return a
		}(), upstream: "session"},
		"Issuer of another IdP": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.issuer = "https://other-idp.example"
			return a
		}(), upstream: "refused"},
		"SubjectConfirmation without data": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.confirmation = `<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"/>`
			return a
		}(), upstream: "panic"},
		"no SubjectConfirmation": {assertion: func() samlTestAssertion {
			a := validSAMLTestAssertion()
			a.confirmation = `<!-- none -->`
			return a
		}(), upstream: "session"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			assertion := fixture.signedAssertion(t, testCase.assertion)
			if testCase.encrypt {
				assertion = fixture.encryptedAssertion(t, testCase.assertion)
			}
			body := samlTestResponse(t, assertion)

			// The same response through the bare library shows the input
			// reaches the GO-2026-6909 flaw it is meant to exercise.
			upstream := func() (outcome string) {
				defer func() {
					if recover() != nil {
						outcome = "panic"
					}
				}()
				response := httptest.NewRecorder()
				bare := *fixture.middleware
				bare.AssertionHandler = samlsp.NopAssertionHandler{}
				bare.ServeHTTP(response, samlACSRequest(url.Values{"SAMLResponse": {body}}))
				if response.Code == http.StatusFound && samlSessionCookie(response) {
					return "session"
				}
				return "refused"
			}()
			if upstream != testCase.upstream {
				t.Fatalf("bare library outcome=%s, want %s; if the library now refuses this, revisit the GO-2026-6909 review entries", upstream, testCase.upstream)
			}

			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, samlACSRequest(url.Values{"SAMLResponse": {body}}))
			if response.Code != http.StatusForbidden || samlSessionCookie(response) {
				t.Fatalf("status=%d cookies=%v body=%s", response.Code, response.Result().Cookies(), response.Body.String())
			}
		})
	}
}

func TestSAMLACSRefusesArtifactBindingAndJunk(t *testing.T) {
	fixture := newSAMLTestFixture(t)
	for name, form := range map[string]url.Values{
		"artifact":          {"SAMLart": {"AAQAAA=="}},
		"not base64":        {"SAMLResponse": {"%%%"}},
		"not XML":           {"SAMLResponse": {base64.StdEncoding.EncodeToString([]byte("not xml"))}},
		"no assertion":      {"SAMLResponse": {base64.StdEncoding.EncodeToString([]byte(`<samlp:Response xmlns:samlp="` + samlProtocolNamespace + `"/>`))}},
		"foreign Assertion": {"SAMLResponse": {base64.StdEncoding.EncodeToString([]byte(`<samlp:Response xmlns:samlp="` + samlProtocolNamespace + `"><Assertion xmlns="urn:example:other"/></samlp:Response>`))}},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, samlACSRequest(form))
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSAMLACSTurnsLibraryPanicIntoRefusal(t *testing.T) {
	fixture := newSAMLTestFixture(t)
	guarded := *fixture.middleware
	guarded.AssertionHandler = panickingAssertionHandler{}
	body := samlTestResponse(t, fixture.signedAssertion(t, validSAMLTestAssertion()))
	response := httptest.NewRecorder()
	guardSAMLACS(&guarded).ServeHTTP(response, samlACSRequest(url.Values{"SAMLResponse": {body}}))
	if response.Code != http.StatusForbidden || samlSessionCookie(response) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type panickingAssertionHandler struct{}

func (panickingAssertionHandler) HandleAssertion(*saml.Assertion) error {
	panic("simulated panic inside the SAML library's ACS handling")
}

func TestSAMLAssertionPolicy(t *testing.T) {
	policy := samlAssertionPolicy{idpEntityID: samlTestIdP}
	valid := func() *saml.Assertion {
		return &saml.Assertion{Issuer: saml.Issuer{Value: samlTestIdP}, Subject: &saml.Subject{NameID: &saml.NameID{Value: "ada", Format: string(saml.PersistentNameIDFormat)}, SubjectConfirmations: []saml.SubjectConfirmation{{Method: "urn:oasis:names:tc:SAML:2.0:cm:bearer", SubjectConfirmationData: &saml.SubjectConfirmationData{}}}}, Conditions: &saml.Conditions{}}
	}
	if err := policy.HandleAssertion(valid()); err != nil {
		t.Fatalf("valid assertion refused: %v", err)
	}
	for name, mutate := range map[string]func(*saml.Assertion){
		"other issuer":    func(a *saml.Assertion) { a.Issuer.Value = "https://other-idp.example" },
		"no issuer":       func(a *saml.Assertion) { a.Issuer = saml.Issuer{} },
		"nil Subject":     func(a *saml.Assertion) { a.Subject = nil },
		"nil NameID":      func(a *saml.Assertion) { a.Subject.NameID = nil },
		"blank NameID":    func(a *saml.Assertion) { a.Subject.NameID.Value = "  " },
		"entity format":   func(a *saml.Assertion) { a.Subject.NameID.Format = "urn:oasis:names:tc:SAML:2.0:nameid-format:entity" },
		"no confirmation": func(a *saml.Assertion) { a.Subject.SubjectConfirmations = nil },
		"holder-of-key only": func(a *saml.Assertion) {
			a.Subject.SubjectConfirmations[0].Method = "urn:oasis:names:tc:SAML:2.0:cm:holder-of-key"
		},
		"confirmation without data": func(a *saml.Assertion) { a.Subject.SubjectConfirmations[0].SubjectConfirmationData = nil },
		"nil Conditions":            func(a *saml.Assertion) { a.Conditions = nil },
	} {
		assertion := valid()
		mutate(assertion)
		if policy.HandleAssertion(assertion) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if (samlAssertionPolicy{}).HandleAssertion(valid()) == nil {
		t.Error("policy without an IdP entity ID accepted an assertion")
	}
}

func TestConfigureSAMLRequiresIdPEntityID(t *testing.T) {
	spKey, spCert := newSAMLTestKey(t, "Flow SAML test SP")
	_, idpCert := newSAMLTestKey(t, "Flow SAML test IdP")
	metadata := `<?xml version="1.0"?><EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata"><IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><KeyDescriptor use="signing"><KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>` + base64.StdEncoding.EncodeToString(idpCert) + `</X509Certificate></X509Data></KeyInfo></KeyDescriptor><SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example/sso"/></IDPSSODescriptor></EntityDescriptor>`
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(spKey)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: spCert})
	if _, err := configureSAML(t.Context(), config.SAMLProvider{Enabled: true, MetadataXML: metadata, EntityID: samlTestSP, SPPrivateKey: string(keyPEM), SPCertificate: string(certPEM)}, samlTestSP); err == nil {
		t.Fatal("metadata without an entityID was accepted")
	}
}
