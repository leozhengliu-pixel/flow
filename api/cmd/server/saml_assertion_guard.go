package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/crewjam/saml/xmlenc"
)

// The SAML library Flow uses (github.com/crewjam/saml, no maintained release
// after v0.5.1) has two assertion-parsing flaws, GO-2026-6909:
//
//   - validateAssertion dereferences Assertion.Subject and Assertion.Conditions
//     without nil checks, so a signed assertion without either element panics
//     inside ServiceProvider.ParseResponse.
//   - its structs decode Subject/NameID, Conditions and the attribute
//     statements by local name only, so an element with the same name in a
//     foreign XML namespace fills the field namespace-aware readers ignore.
//
// No fixed version exists, so the ACS endpoint checks the raw response before
// the library parses it (guardSAMLACS) and checks the parsed assertion again
// before a session is created (samlAssertionPolicy). Both only ever reject.

const (
	samlAssertionNamespace = "urn:oasis:names:tc:SAML:2.0:assertion"
	samlProtocolNamespace  = "urn:oasis:names:tc:SAML:2.0:protocol"
	xmlEncryptionNamespace = "http://www.w3.org/2001/04/xmlenc#"
	samlBearerConfirmation = "urn:oasis:names:tc:SAML:2.0:cm:bearer"
)

// samlDecodedElements are the assertion elements the library decodes by
// local name. Each must be in the SAML assertion namespace wherever it
// appears inside an assertion.
var samlDecodedElements = map[string]bool{
	"Issuer": true, "Subject": true, "NameID": true, "SubjectConfirmation": true, "SubjectConfirmationData": true,
	"Conditions": true, "AudienceRestriction": true, "Audience": true, "OneTimeUse": true, "ProxyRestriction": true,
	"AuthnStatement": true, "AuthnContext": true, "AuthnContextClassRef": true, "SubjectLocality": true,
	"AttributeStatement": true, "Attribute": true, "AttributeValue": true,
}

// samlUserNameIDFormats are the NameID formats that name a person. Flow asks
// for transient; IdPs commonly answer with another of these. The entity format
// names a system entity, not a user, and is refused. The two 2.0-prefixed
// variants of the 1.1 formats are common IdP mistakes and harmless here,
// because Flow maps the user from attributes, never from the NameID.
var samlUserNameIDFormats = map[string]bool{
	"": true,
	"urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified":                true,
	"urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress":               true,
	"urn:oasis:names:tc:SAML:1.1:nameid-format:X509SubjectName":            true,
	"urn:oasis:names:tc:SAML:1.1:nameid-format:WindowsDomainQualifiedName": true,
	"urn:oasis:names:tc:SAML:2.0:nameid-format:kerberos":                   true,
	"urn:oasis:names:tc:SAML:2.0:nameid-format:persistent":                 true,
	"urn:oasis:names:tc:SAML:2.0:nameid-format:transient":                  true,
	"urn:oasis:names:tc:SAML:2.0:nameid-format:unspecified":                true,
	"urn:oasis:names:tc:SAML:2.0:nameid-format:emailAddress":               true,
}

// guardSAMLACS wraps the library's ACS handler. It refuses the artifact
// binding (Flow requests HTTP-POST responses, and an artifact response cannot
// be checked before the library parses it), checks the posted response, and
// turns any panic left in the library's parser into an ordinary refusal.
func guardSAMLACS(middleware *samlsp.Middleware) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			middleware.OnError(w, r, samlRefusal(errors.New("cannot parse the ACS form")))
			return
		}
		if r.Form.Get("SAMLart") != "" {
			middleware.OnError(w, r, samlRefusal(errors.New("the SAML artifact binding is not accepted")))
			return
		}
		raw, err := base64.StdEncoding.DecodeString(r.PostForm.Get("SAMLResponse"))
		if err != nil {
			middleware.OnError(w, r, samlRefusal(errors.New("SAMLResponse is not base64")))
			return
		}
		if err := checkSAMLResponseXML(raw, &middleware.ServiceProvider); err != nil {
			middleware.OnError(w, r, samlRefusal(err))
			return
		}
		writer := &panicRecoveryWriter{ResponseWriter: w}
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(value)
			}
			if writer.wroteHeader || writer.hijacked {
				panic(http.ErrAbortHandler)
			}
			middleware.OnError(w, r, samlRefusal(fmt.Errorf("the SAML library panicked while parsing the response: %s", panicSummary(value))))
		}()
		middleware.ServeHTTP(writer, r)
	})
}

func samlRefusal(err error) error {
	return &saml.InvalidResponseError{PrivateErr: err, Now: saml.TimeNow()}
}

// checkSAMLResponseXML checks every assertion the library would read from the
// response: the plaintext ones and, decrypted with the SP key, the encrypted
// ones. It parses with the same XML library the SAML library uses.
func checkSAMLResponseXML(raw []byte, sp *saml.ServiceProvider) error {
	if sp.IDPMetadata == nil || sp.IDPMetadata.EntityID == "" {
		return errors.New("the IdP entity ID is not configured")
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(raw); err != nil {
		return errors.New("cannot parse the SAML response")
	}
	root := doc.Root()
	if root == nil || root.Tag != "Response" || root.NamespaceURI() != samlProtocolNamespace {
		return errors.New("the document is not a SAML protocol Response")
	}
	assertions := 0
	for _, child := range root.ChildElements() {
		switch child.Tag {
		case "Assertion", "EncryptedAssertion":
		default:
			continue
		}
		if child.NamespaceURI() != samlAssertionNamespace {
			return fmt.Errorf("%s is outside the SAML assertion namespace", child.Tag)
		}
		assertion := child
		if child.Tag == "EncryptedAssertion" {
			decrypted, err := decryptSAMLAssertion(child, sp.Key)
			if err != nil {
				return err
			}
			assertion = decrypted
		}
		if err := checkSAMLAssertionXML(assertion, sp.IDPMetadata.EntityID); err != nil {
			return err
		}
		assertions++
	}
	if assertions == 0 {
		return errors.New("the SAML response has no assertion")
	}
	return nil
}

// decryptSAMLAssertion mirrors the library's decryption of an
// EncryptedAssertion so the plaintext can be checked before it is parsed.
func decryptSAMLAssertion(encrypted *etree.Element, key any) (*etree.Element, error) {
	var data *etree.Element
	for _, child := range encrypted.ChildElements() {
		if child.Tag == "EncryptedData" && child.NamespaceURI() == xmlEncryptionNamespace {
			if data != nil {
				return nil, errors.New("EncryptedAssertion has more than one EncryptedData")
			}
			data = child
		}
	}
	if data == nil {
		return nil, errors.New("EncryptedAssertion has no EncryptedData")
	}
	blockKey := key
	if keyElement := encrypted.FindElement("./EncryptedKey"); keyElement != nil {
		decryptedKey, err := xmlenc.Decrypt(key, keyElement)
		if err != nil {
			return nil, errors.New("cannot decrypt the assertion key")
		}
		blockKey = decryptedKey
	}
	plaintext, err := xmlenc.Decrypt(blockKey, data)
	if err != nil {
		return nil, errors.New("cannot decrypt the assertion")
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(plaintext); err != nil || doc.Root() == nil {
		return nil, errors.New("cannot parse the decrypted assertion")
	}
	root := doc.Root()
	if root.Tag != "Assertion" || root.NamespaceURI() != samlAssertionNamespace {
		return nil, errors.New("the decrypted element is not a SAML assertion")
	}
	return root, nil
}

func checkSAMLAssertionXML(assertion *etree.Element, idpEntityID string) error {
	if err := checkSAMLElementNamespaces(assertion); err != nil {
		return err
	}
	issuer, err := oneSAMLChild(assertion, "Issuer")
	if err != nil {
		return err
	}
	if issuer.Text() != idpEntityID {
		return errors.New("the assertion Issuer is not the configured IdP")
	}
	subject, err := oneSAMLChild(assertion, "Subject")
	if err != nil {
		return err
	}
	nameID, err := oneSAMLChild(subject, "NameID")
	if err != nil {
		return err
	}
	if strings.TrimSpace(nameID.Text()) == "" {
		return errors.New("the assertion NameID is empty")
	}
	if !samlUserNameIDFormats[nameID.SelectAttrValue("Format", "")] {
		return errors.New("the assertion NameID format does not name a user")
	}
	bearer := false
	for _, confirmation := range samlChildren(subject, "SubjectConfirmation") {
		// The library also dereferences SubjectConfirmationData unchecked.
		if _, err := oneSAMLChild(confirmation, "SubjectConfirmationData"); err != nil {
			return err
		}
		bearer = bearer || confirmation.SelectAttrValue("Method", "") == samlBearerConfirmation
	}
	if !bearer {
		return errors.New("the assertion Subject has no bearer SubjectConfirmation")
	}
	if _, err := oneSAMLChild(assertion, "Conditions"); err != nil {
		return err
	}
	return nil
}

// checkSAMLElementNamespaces refuses any element the library decodes by
// local name that is not in the SAML assertion namespace.
func checkSAMLElementNamespaces(element *etree.Element) error {
	for _, child := range element.ChildElements() {
		if samlDecodedElements[child.Tag] && child.NamespaceURI() != samlAssertionNamespace {
			return fmt.Errorf("%s is outside the SAML assertion namespace", child.Tag)
		}
		if err := checkSAMLElementNamespaces(child); err != nil {
			return err
		}
	}
	return nil
}

func samlChildren(parent *etree.Element, tag string) []*etree.Element {
	var children []*etree.Element
	for _, child := range parent.ChildElements() {
		if child.Tag == tag && child.NamespaceURI() == samlAssertionNamespace {
			children = append(children, child)
		}
	}
	return children
}

func oneSAMLChild(parent *etree.Element, tag string) (*etree.Element, error) {
	children := samlChildren(parent, tag)
	switch len(children) {
	case 0:
		return nil, fmt.Errorf("%s has no %s", parent.Tag, tag)
	case 1:
		return children[0], nil
	default:
		return nil, fmt.Errorf("%s has more than one %s", parent.Tag, tag)
	}
}

// samlAssertionPolicy checks the assertion the library parsed before a
// session is created from it, whatever path it came through.
type samlAssertionPolicy struct {
	idpEntityID string
}

func (p samlAssertionPolicy) HandleAssertion(assertion *saml.Assertion) error {
	switch {
	case p.idpEntityID == "" || assertion.Issuer.Value != p.idpEntityID:
		return samlRefusal(errors.New("the assertion Issuer is not the configured IdP"))
	case assertion.Subject == nil || assertion.Subject.NameID == nil || strings.TrimSpace(assertion.Subject.NameID.Value) == "":
		return samlRefusal(errors.New("the assertion has no NameID"))
	case !samlUserNameIDFormats[assertion.Subject.NameID.Format]:
		return samlRefusal(errors.New("the assertion NameID format does not name a user"))
	case !hasBearerConfirmation(assertion.Subject.SubjectConfirmations):
		return samlRefusal(errors.New("the assertion Subject has no bearer SubjectConfirmation"))
	case assertion.Conditions == nil:
		return samlRefusal(errors.New("the assertion has no Conditions"))
	}
	return nil
}

// hasBearerConfirmation reports whether the confirmations include a bearer
// one and every confirmation carries its data element.
func hasBearerConfirmation(confirmations []saml.SubjectConfirmation) bool {
	bearer := false
	for _, confirmation := range confirmations {
		if confirmation.SubjectConfirmationData == nil {
			return false
		}
		bearer = bearer || confirmation.Method == samlBearerConfirmation
	}
	return bearer
}
