package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	// The event package depends only on the SDK core, so the reference
	// implementation stays usable here without the full SDK service tree.
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
)

// chatCallbackSeal encrypts raw (already padded or not) the way the platform
// does: base64(iv || AES-256-CBC(raw)) with key SHA-256(encryptKey).
func chatCallbackSeal(t *testing.T, raw []byte, encryptKey string) string {
	t.Helper()
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	// A deterministic IV keeps the reference comparisons for garbled inputs
	// reproducible run to run.
	seed := sha256.Sum256(raw)
	iv := seed[:aes.BlockSize]
	out := make([]byte, len(raw))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, raw)
	return base64.StdEncoding.EncodeToString(append(append([]byte{}, iv...), out...))
}

func chatCallbackPad(plain []byte) []byte {
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	return append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func chatCallbackBody(encrypt string) []byte {
	raw, _ := json.Marshal(map[string]string{"encrypt": encrypt})
	return raw
}

// sdkCallbackPlaintext mirrors the reference dispatcher's ParseReq and
// DecryptEvent steps followed by its JSON parse.
func sdkCallbackPlaintext(body []byte, encryptKey string) ([]byte, bool) {
	var msg larkevent.EventEncryptMsg
	if json.Unmarshal(body, &msg) != nil || msg.Encrypt == "" {
		return nil, false
	}
	plain, err := larkevent.EventDecrypt(msg.Encrypt, encryptKey)
	if err != nil || !json.Valid(plain) {
		return nil, false
	}
	return plain, true
}

func TestChatCallbackDecryptMatchesReference(t *testing.T) {
	const key = "encrypt_key_cross_check"
	// Every padding length 1..16 and multi-block payloads, including unicode
	// and escaped braces that the reference's brace trimming must not disturb.
	for n := 0; n <= 70; n++ {
		value := map[string]any{"schema": "2.0", "type": "event_callback", "pad": strings.Repeat("x", n), "text": "创建事项 {braces}"}
		plain, _ := json.Marshal(value)
		body := chatCallbackBody(chatCallbackSeal(t, chatCallbackPad(plain), key))
		ours, err := chatCallbackPlaintext(body, key)
		ref, ok := sdkCallbackPlaintext(body, key)
		if err != nil || !ok || !bytes.Equal(ours, ref) || !bytes.Equal(ours, plain) {
			t.Fatalf("n=%d: ours=%q err=%v ref=%q ok=%v", n, ours, err, ref, ok)
		}
	}
	url := []byte(`{"type":"url_verification","token":"verify","challenge":"c"}`)
	body := chatCallbackBody(chatCallbackSeal(t, chatCallbackPad(url), key))
	if got, err := chatCallbackPlaintext(body, key); err != nil || !bytes.Equal(got, url) {
		t.Fatalf("challenge: %q %v", got, err)
	}
	// Extra envelope fields are ignored by both.
	var withExtra map[string]any
	_ = json.Unmarshal(body, &withExtra)
	withExtra["other"] = 1
	extra, _ := json.Marshal(withExtra)
	if _, err := chatCallbackPlaintext(extra, key); err != nil {
		t.Fatalf("extra field: %v", err)
	}
	if _, ok := sdkCallbackPlaintext(extra, key); !ok {
		t.Fatal("reference rejected extra field")
	}
}

func TestChatCallbackDecryptRejects(t *testing.T) {
	const key = "encrypt_key_cross_check"
	plain := []byte(`{"schema":"2.0","header":{"token":"t"}}`)
	valid := chatCallbackSeal(t, chatCallbackPad(plain), key)
	raw, _ := base64.StdEncoding.DecodeString(valid)
	flipped := append([]byte{}, raw...)
	flipped[len(flipped)-1] ^= 0x01
	badPad := func(tail []byte) string {
		data := append(append([]byte{}, plain...), tail...)
		return chatCallbackSeal(t, data, key)
	}
	cases := []struct {
		name string
		body []byte
		key  string
		// refAccepts records the reference behaviour; the in-repo decoder must
		// reject every case either way.
		refAccepts bool
	}{
		{"not json", []byte("encrypt=abc"), key, false},
		{"plaintext event", plain, key, false},
		{"blank encrypt", chatCallbackBody(""), key, false},
		{"bad base64", chatCallbackBody("!!!not-base64!!!"), key, false},
		{"iv only", chatCallbackBody(base64.StdEncoding.EncodeToString(raw[:aes.BlockSize])), key, false},
		{"short", chatCallbackBody(base64.StdEncoding.EncodeToString(raw[:10])), key, false},
		{"partial block", chatCallbackBody(base64.StdEncoding.EncodeToString(raw[:len(raw)-3])), key, false},
		{"wrong key", chatCallbackBody(valid), "another_key", false},
		{"no encrypt key", chatCallbackBody(valid), "", false},
		{"flipped last byte", chatCallbackBody(base64.StdEncoding.EncodeToString(flipped)), key, false},
		// The reference trims to the outermost braces and ignores padding;
		// the in-repo decoder requires exact PKCS#7.
		{"zero padding byte", chatCallbackBody(badPad(bytes.Repeat([]byte{0}, aes.BlockSize-len(plain)%aes.BlockSize))), key, true},
		{"padding above block size", chatCallbackBody(badPad(append(bytes.Repeat([]byte{17}, 2*aes.BlockSize-len(plain)%aes.BlockSize-1), 17))), key, true},
		{"inconsistent padding", chatCallbackBody(badPad(func() []byte {
			n := aes.BlockSize - len(plain)%aes.BlockSize
			tail := bytes.Repeat([]byte{byte(n)}, n)
			tail[0] = 'z'
			return tail
		}())), key, true},
		{"padding hides trailing garbage", chatCallbackBody(chatCallbackSeal(t, chatCallbackPad(append(append([]byte{}, plain...), "garbage"...)), key)), key, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := chatCallbackPlaintext(tc.body, tc.key); err == nil {
				t.Fatalf("accepted: %q", got)
			}
			if tc.key == "" {
				return
			}
			if _, ok := sdkCallbackPlaintext(tc.body, tc.key); ok != tc.refAccepts {
				t.Fatalf("reference accepted=%v, expected %v", ok, tc.refAccepts)
			}
		})
	}
}

func TestChatCallbackSignatureMatchesReference(t *testing.T) {
	const key = "encrypt_key_cross_check"
	body := chatCallbackBody(chatCallbackSeal(t, chatCallbackPad([]byte(`{"schema":"2.0"}`)), key))
	stamp, nonce := strconv.Itoa(1767225600), "nonce_cross_check"
	signed := func(edit func(http.Header)) http.Header {
		h := http.Header{}
		h.Set(chatCallbackTimestampHeader, stamp)
		h.Set(chatCallbackNonceHeader, nonce)
		h.Set(chatCallbackSignatureHeader, larkevent.Signature(stamp, nonce, key, string(body)))
		if edit != nil {
			edit(h)
		}
		return h
	}
	// The reference dispatcher accepts exactly when the first header value
	// equals Signature(timestamp, nonce, key, body).
	reference := func(h http.Header, b []byte, k string) bool {
		return k != "" && h.Get(chatCallbackSignatureHeader) == larkevent.Signature(h.Get(chatCallbackTimestampHeader), h.Get(chatCallbackNonceHeader), k, string(b))
	}
	cases := []struct {
		name   string
		header http.Header
		body   []byte
		key    string
		want   bool
	}{
		{"valid", signed(nil), body, key, true},
		{"tampered body", signed(nil), append(append([]byte{}, body...), ' '), key, false},
		{"tampered timestamp", signed(func(h http.Header) { h.Set(chatCallbackTimestampHeader, "1767225601") }), body, key, false},
		{"tampered nonce", signed(func(h http.Header) { h.Set(chatCallbackNonceHeader, "other") }), body, key, false},
		{"wrong key", signed(nil), body, "another_key", false},
		{"missing signature", signed(func(h http.Header) { h.Del(chatCallbackSignatureHeader) }), body, key, false},
		{"uppercase hex", signed(func(h http.Header) {
			h.Set(chatCallbackSignatureHeader, strings.ToUpper(h.Get(chatCallbackSignatureHeader)))
		}), body, key, false},
		{"truncated", signed(func(h http.Header) { h.Set(chatCallbackSignatureHeader, h.Get(chatCallbackSignatureHeader)[:63]) }), body, key, false},
		{"second value only", signed(func(h http.Header) {
			good := h.Get(chatCallbackSignatureHeader)
			h.Set(chatCallbackSignatureHeader, "bad")
			h.Add(chatCallbackSignatureHeader, good)
		}), body, key, false},
		{"no encrypt key", signed(nil), body, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatCallbackSignatureValid(tc.header, tc.body, tc.key); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if got := reference(tc.header, tc.body, tc.key); got != tc.want {
				t.Fatalf("reference got %v want %v", got, tc.want)
			}
		})
	}
}
