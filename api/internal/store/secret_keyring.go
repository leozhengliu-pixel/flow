package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Secrets at rest — connector credentials, integration OAuth tokens, webhook
// signing secrets and audit stream secrets — are sealed with AES-256-GCM
// under FLOW_CONNECTOR_SECRET_KEY, each bound to its row (the id or ref is
// the additional authenticated data), so a ciphertext copied to another row
// does not open.
//
// FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS lists keys that were current before a
// rotation. They only open existing ciphertexts; everything new is sealed
// under the current key, and ResealSecrets rewrites what an old key sealed.
// Both variables accept *_FILE.
const (
	connectorSecretKeyEnv      = "FLOW_CONNECTOR_SECRET_KEY"
	connectorPreviousKeysEnv   = "FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS"
	connectorSecretKeyRequired = "FLOW_CONNECTOR_SECRET_KEY must be a base64-encoded 32-byte key"
)

// ErrSecretKeyMissing is returned when a secret must be sealed but no valid
// FLOW_CONNECTOR_SECRET_KEY is configured. Secrets are never stored in plain
// text instead.
var ErrSecretKeyMissing = errors.New(connectorSecretKeyRequired)

func secretKeyCipher(encoded string) (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != 32 {
		return nil, ErrSecretKeyMissing
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// connectorCipher is the current key, which seals every new secret.
func connectorCipher() (cipher.AEAD, error) {
	return secretKeyCipher(readSecretEnv(connectorSecretKeyEnv))
}

func previousKeyValues() []string {
	return strings.FieldsFunc(readSecretEnv(connectorPreviousKeysEnv), func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// previousConnectorCiphers are the retired keys, newest first as listed.
// Malformed entries are skipped here; ValidateSecretKeys reports them at
// startup.
func previousConnectorCiphers() []cipher.AEAD {
	result := []cipher.AEAD{}
	for _, value := range previousKeyValues() {
		if aead, err := secretKeyCipher(value); err == nil {
			result = append(result, aead)
		}
	}
	return result
}

// ValidateSecretKeys reports a key configuration that cannot work: a key
// that is set but malformed, or retired keys without a current key.
func ValidateSecretKeys() error {
	current := readSecretEnv(connectorSecretKeyEnv)
	if current != "" {
		if _, err := secretKeyCipher(current); err != nil {
			return fmt.Errorf("%s: %w", connectorSecretKeyEnv, err)
		}
	}
	previous := previousKeyValues()
	for index, value := range previous {
		if _, err := secretKeyCipher(value); err != nil {
			return fmt.Errorf("%s entry %d must be a base64-encoded 32-byte key", connectorPreviousKeysEnv, index+1)
		}
	}
	if current == "" && len(previous) > 0 {
		return fmt.Errorf("%s is set without %s", connectorPreviousKeysEnv, connectorSecretKeyEnv)
	}
	return nil
}

// sealSecret encrypts plain under the current key as nonce||ciphertext.
func sealSecret(plain, aad []byte) ([]byte, error) {
	aead, err := connectorCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, aad), nil
}

var errSecretUnreadable = errors.New("stored secret cannot be decrypted with the configured keys")

// openSecret decrypts nonce||ciphertext with the current key, then with each
// retired key. current reports whether the current key opened it (false
// means ResealSecrets should rewrite it).
func openSecret(sealed, aad []byte) (plain []byte, current bool, err error) {
	aead, err := connectorCipher()
	if err != nil {
		return nil, false, err
	}
	if plain, ok := openWith(aead, sealed, aad); ok {
		return plain, true, nil
	}
	for _, previous := range previousConnectorCiphers() {
		if plain, ok := openWith(previous, sealed, aad); ok {
			return plain, false, nil
		}
	}
	return nil, false, errSecretUnreadable
}

func openWith(aead cipher.AEAD, sealed, aad []byte) ([]byte, bool) {
	if len(sealed) < aead.NonceSize() {
		return nil, false
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	return plain, err == nil
}

// sealedTextPrefix marks a sealed secret in a text column; a value without it
// was stored in plain text by an older release (see openSecretText).
const sealedTextPrefix = "v1:"

// sealSecretText seals a secret for a text column.
func sealSecretText(plain, aad string) (string, error) {
	sealed, err := sealSecret([]byte(plain), []byte(aad))
	if err != nil {
		return "", err
	}
	return sealedTextPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// openSecretText reads a text-column secret. A value an older release stored
// in plain text (no prefix) is returned as is with stale set, so it keeps
// working until ResealSecrets seals it; stale is also set for a value a
// retired key sealed.
func openSecretText(stored, aad string) (plain string, stale bool, err error) {
	if !strings.HasPrefix(stored, sealedTextPrefix) {
		return stored, true, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealedTextPrefix))
	if err != nil {
		return "", false, errSecretUnreadable
	}
	opened, current, err := openSecret(raw, []byte(aad))
	if err != nil {
		return "", false, err
	}
	return string(opened), !current, nil
}
