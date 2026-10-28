package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

// Feishu and Lark event callbacks share one wire format. With an encrypt key
// configured, the body is {"encrypt": base64(iv || AES-256-CBC(plaintext))},
// where the AES key is SHA-256(encrypt key) and the plaintext carries PKCS#7
// padding. Requests are signed with hex(SHA-256(timestamp || nonce || encrypt
// key || raw body)) in X-Lark-Signature.
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
