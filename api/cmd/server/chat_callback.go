package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"flow/api/internal/domain"
)

// Feishu and Lark event callbacks share one wire format. With an encrypt key
// configured, the body is {"encrypt": base64(iv || AES-256-CBC(plaintext))},
// where the AES key is SHA-256(encrypt key) and the plaintext carries PKCS#7
// padding. Requests are signed with hex(SHA-256(timestamp || nonce || encrypt
// key || raw body)) in X-Lark-Signature.
//
// Card clicks reach the same URL in one of two formats, depending on which
// callback the app's console subscribes:
//   - card.action.trigger (schema 2.0, like every event): encrypted when an
//     encrypt key is set, signed as above, header.token is the verification
//     token;
//   - the legacy card request (a card request URL, or card.action.trigger_v1
//     in the callback configuration): no schema, the click's fields at the
//     top level, often plain JSON, signed with hex(SHA-1(timestamp || nonce
//     || verification token || raw body)). Its top-level token is the card's
//     update token, not the verification token.
//
// Both are accepted (chatVerifyCallback). Every rejection is logged with a
// reason code, never with a token, key, signature or payload.
const (
	chatCallbackTimestampHeader = "X-Lark-Request-Timestamp"
	chatCallbackNonceHeader     = "X-Lark-Request-Nonce"
	chatCallbackSignatureHeader = "X-Lark-Signature"
)

var errChatCallbackInvalid = errors.New("invalid chat callback")

// chatCallbackPlaintext returns the decrypted JSON event of an encrypted
// callback body. Every failure returns the same error so callers cannot leak
// which step (envelope, base64, length, padding, JSON) rejected the request.
func chatCallbackPlaintext(body []byte, encryptKey string) ([]byte, error) {
	if encryptKey == "" {
		return nil, errChatCallbackInvalid
	}
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Encrypt == "" {
		return nil, errChatCallbackInvalid
	}
	raw, err := base64.StdEncoding.DecodeString(envelope.Encrypt)
	// One block of IV followed by at least one whole block of ciphertext.
	if err != nil || len(raw) < 2*aes.BlockSize || len(raw)%aes.BlockSize != 0 {
		return nil, errChatCallbackInvalid
	}
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, errChatCallbackInvalid
	}
	iv, data := raw[:aes.BlockSize], raw[aes.BlockSize:]
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(data, data)
	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize {
		return nil, errChatCallbackInvalid
	}
	if subtle.ConstantTimeCompare(data[len(data)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) != 1 {
		return nil, errChatCallbackInvalid
	}
	plaintext := data[:len(data)-padding]
	if !json.Valid(plaintext) {
		return nil, errChatCallbackInvalid
	}
	return plaintext, nil
}

// chatCallbackSignatureValid reports whether the request signature matches
// the raw body. Freshness of the timestamp and presence of the nonce are
// checked by the caller.
func chatCallbackSignatureValid(header http.Header, body []byte, encryptKey string) bool {
	if encryptKey == "" {
		return false
	}
	hash := sha256.New()
	for _, part := range [][]byte{[]byte(header.Get(chatCallbackTimestampHeader)), []byte(header.Get(chatCallbackNonceHeader)), []byte(encryptKey), body} {
		_, _ = hash.Write(part)
	}
	expected := hex.EncodeToString(hash.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(header.Get(chatCallbackSignatureHeader))) == 1
}

// chatLegacyCardSignatureValid checks the legacy card request signature:
// hex(SHA-1(timestamp || nonce || verification token || raw body)).
func chatLegacyCardSignatureValid(header http.Header, body []byte, verificationToken string) bool {
	if verificationToken == "" {
		return false
	}
	hash := sha1.New()
	for _, part := range [][]byte{[]byte(header.Get(chatCallbackTimestampHeader)), []byte(header.Get(chatCallbackNonceHeader)), []byte(verificationToken), body} {
		_, _ = hash.Write(part)
	}
	expected := hex.EncodeToString(hash.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(header.Get(chatCallbackSignatureHeader))) == 1
}

// Callback formats, as the diagnostics name them.
const (
	chatCallbackFormatEvent      = "event_v2"
	chatCallbackFormatLegacyCard = "legacy_card"
	chatCallbackFormatChallenge  = "url_verification"
	chatCallbackFormatUnknown    = "unknown"
	// chatLegacyCardEventType names a legacy card request in the
	// diagnostics; it is handled as card.action.trigger.
	chatLegacyCardEventType = "card.action.trigger_v1"
)

// chatCallbackWindow is how far the request timestamp may be from now.
const chatCallbackWindow = 5 * time.Minute

// Rejection reason codes of chatVerifyCallback, in the order of the checks.
const (
	chatRejectMalformed         = "malformed"          // not JSON, or a legacy card request without its action
	chatRejectDecrypt           = "decrypt"            // an encrypted body the encrypt key does not open
	chatRejectChallengeToken    = "challenge_token"    // url_verification with another verification token
	chatRejectNotConnected      = "not_connected"      // the tenant authorization is not current
	chatRejectTimestamp         = "timestamp"          // missing, unparsable or outside the window
	chatRejectNonce             = "nonce"              // missing nonce
	chatRejectSignature         = "signature"          // no signature scheme matches the raw body
	chatRejectToken             = "token"              // schema 2.0 header.token is not the verification token
	chatRejectAppMismatch       = "app_mismatch"       // an event of another app
	chatRejectTenantMismatch    = "tenant_mismatch"    // an event of another tenant
	chatRejectUnsupportedFormat = "unsupported_format" // neither schema 2.0 nor a legacy card request
)

// chatRejectStatus is the HTTP status of a rejection.
func chatRejectStatus(reason string) int {
	switch reason {
	case chatRejectNotConnected, chatRejectAppMismatch, chatRejectTenantMismatch, chatRejectUnsupportedFormat:
		return http.StatusForbidden
	}
	return http.StatusUnauthorized
}

// chatLegacyCardCallback is the body of a legacy card request.
type chatLegacyCardCallback struct {
	OpenID        string `json:"open_id"`
	OpenMessageID string `json:"open_message_id"`
	OpenChatID    string `json:"open_chat_id"`
	TenantKey     string `json:"tenant_key"`
	Action        *struct {
		Value     map[string]json.RawMessage `json:"value"`
		FormValue map[string]json.RawMessage `json:"form_value"`
		Option    string                     `json:"option"`
	} `json:"action"`
}

// chatVerifiedCallback is an authenticated callback, normalized to the
// schema 2.0 envelope.
type chatVerifiedCallback struct {
	Envelope  chatWebhookEnvelope
	Plaintext []byte
	Format    string
	Encrypted bool
	// Scheme is the signature that matched: sha256_encrypt_key or
	// sha1_verification_token.
	Scheme string
}

// chatCallbackTimestamp parses the request timestamp: Unix seconds, or
// milliseconds when the value is that large.
func chatCallbackTimestamp(value string) (time.Time, bool) {
	stamp, err := strconv.ParseInt(value, 10, 64)
	if err != nil || stamp <= 0 {
		return time.Time{}, false
	}
	if stamp > 1e11 {
		return time.UnixMilli(stamp), true
	}
	return time.Unix(stamp, 0), true
}

// chatVerifyCallback authenticates a callback for record and returns it, or
// the reason code that rejected it. On a rejection the result still carries
// what the diagnostics may log: format, encryption and the event type and app
// ID once the body is readable.
func chatVerifyCallback(header http.Header, body []byte, credentials chatCredentials, record domain.ChatIntegrationRecord, now time.Time) (chatVerifiedCallback, string) {
	result := chatVerifiedCallback{Format: chatCallbackFormatUnknown}
	var outer struct {
		Encrypt string `json:"encrypt"`
	}
	if json.Unmarshal(body, &outer) != nil {
		return result, chatRejectMalformed
	}
	result.Plaintext = body
	if outer.Encrypt != "" {
		result.Encrypted = true
		plaintext, err := chatCallbackPlaintext(body, credentials.EncryptKey)
		if err != nil {
			return result, chatRejectDecrypt
		}
		result.Plaintext = plaintext
	}
	// A plain body is authenticated by its signature below, like an
	// encrypted one: both schemes need a secret of the app.
	var fields struct {
		Schema    string          `json:"schema"`
		Type      string          `json:"type"`
		Header    json.RawMessage `json:"header"`
		Action    json.RawMessage `json:"action"`
		MessageID string          `json:"open_message_id"`
	}
	if json.Unmarshal(result.Plaintext, &fields) != nil || json.Unmarshal(result.Plaintext, &result.Envelope) != nil {
		return result, chatRejectMalformed
	}
	envelope := &result.Envelope
	switch {
	case fields.Type == "url_verification":
		result.Format = chatCallbackFormatChallenge
	case fields.Schema == "2.0" && len(fields.Header) > 0:
		result.Format = chatCallbackFormatEvent
	case fields.Schema == "" && len(fields.Action) > 0 && fields.MessageID != "":
		result.Format = chatCallbackFormatLegacyCard
		envelope.Header.EventType = chatLegacyCardEventType
	}
	if result.Format == chatCallbackFormatChallenge {
		// The platform verifies a URL before it signs anything there; the
		// verification token is the proof, encrypted or not.
		if envelope.Challenge == "" || len(envelope.Challenge) > 512 || subtle.ConstantTimeCompare([]byte(envelope.Token), []byte(credentials.VerificationToken)) != 1 {
			return result, chatRejectChallengeToken
		}
		return result, ""
	}
	if !record.Connected {
		return result, chatRejectNotConnected
	}
	stamp, ok := chatCallbackTimestamp(header.Get(chatCallbackTimestampHeader))
	if !ok || now.Sub(stamp) > chatCallbackWindow || stamp.Sub(now) > chatCallbackWindow {
		return result, chatRejectTimestamp
	}
	if header.Get(chatCallbackNonceHeader) == "" {
		return result, chatRejectNonce
	}
	switch {
	case chatCallbackSignatureValid(header, body, credentials.EncryptKey):
		result.Scheme = "sha256_encrypt_key"
	case result.Format == chatCallbackFormatLegacyCard && chatLegacyCardSignatureValid(header, body, credentials.VerificationToken):
		result.Scheme = "sha1_verification_token"
	default:
		return result, chatRejectSignature
	}
	switch result.Format {
	case chatCallbackFormatEvent:
		if subtle.ConstantTimeCompare([]byte(envelope.Header.Token), []byte(credentials.VerificationToken)) != 1 {
			return result, chatRejectToken
		}
		if envelope.Header.AppID != record.AppID {
			return result, chatRejectAppMismatch
		}
		if envelope.Header.TenantKey != record.TenantKey {
			return result, chatRejectTenantMismatch
		}
	case chatCallbackFormatLegacyCard:
		// The signature names the app (its verification token or encrypt
		// key) and the URL the connection; the body names no app.
		var legacy chatLegacyCardCallback
		if json.Unmarshal(result.Plaintext, &legacy) != nil || legacy.Action == nil {
			return result, chatRejectMalformed
		}
		if legacy.TenantKey != record.TenantKey {
			return result, chatRejectTenantMismatch
		}
		*envelope = chatWebhookEnvelope{Schema: "2.0", legacyCard: true}
		envelope.Header.AppID, envelope.Header.TenantKey, envelope.Header.EventType = record.AppID, legacy.TenantKey, "card.action.trigger"
		// It has no event ID: the request's timestamp and nonce identify
		// the click for the callback dedup.
		envelope.Header.EventID = "legacy_" + chatKey(header.Get(chatCallbackTimestampHeader), header.Get(chatCallbackNonceHeader), legacy.OpenMessageID)[:32]
		envelope.Event.Operator.TenantKey, envelope.Event.Operator.OpenID = legacy.TenantKey, legacy.OpenID
		envelope.Event.Context.MessageID, envelope.Event.Context.ChatID = legacy.OpenMessageID, legacy.OpenChatID
		envelope.Event.Action.Value, envelope.Event.Action.FormValue, envelope.Event.Action.Option = legacy.Action.Value, legacy.Action.FormValue, legacy.Action.Option
	default:
		return result, chatRejectUnsupportedFormat
	}
	return result, ""
}
