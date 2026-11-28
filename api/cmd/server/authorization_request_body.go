package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"flow/api/internal/telemetry"
)

var errAuthorizationJSON = errors.New("invalid JSON")

type authorizationBodyKey struct{}

// The first body-dependent check reads the complete JSON object. Authorization
// can project it into several input types, but the handler reuses the same
// decode when it takes the same type. A failed check poisons the request even
// if the permission predicate would otherwise allow a fallback.
type authorizationBody struct {
	raw     []byte
	err     error
	decoded map[authorizationJSONType]reflect.Value
}

type authorizationJSONType struct {
	target reflect.Type
	strict bool
}

func requestAuthorizationBody(r *http.Request) *authorizationBody {
	body, _ := r.Context().Value(authorizationBodyKey{}).(*authorizationBody)
	return body
}

func bufferedAuthorizationJSON(r *http.Request) *authorizationBody {
	if body := requestAuthorizationBody(r); body != nil && (body.raw != nil || body.err != nil) {
		return body
	}
	return nil
}

func authorizationBodyError(r *http.Request) error {
	if document := requestDocumentBody(r); document != nil && document.err != nil {
		return document.err
	}
	if body := requestAuthorizationBody(r); body != nil {
		return body.err
	}
	return nil
}

func prepareAuthorizationBody(r *http.Request) *authorizationBody {
	if body := requestAuthorizationBody(r); body != nil {
		return body
	}
	*r = *withAuthorizationBody(r)
	return requestAuthorizationBody(r)
}

func withAuthorizationBody(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authorizationBodyKey{}, &authorizationBody{}))
}

func unsupportedJSONEncoding(r *http.Request) bool {
	for _, value := range r.Header.Values("Content-Encoding") {
		if value = strings.TrimSpace(value); value != "" && !strings.EqualFold(value, "identity") {
			return true
		}
	}
	return false
}

func readAuthorizationBody(r *http.Request) ([]byte, error) {
	limit := jsonRequestLimit()
	if r.ContentLength > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}
	if r.Body == nil || unsupportedJSONEncoding(r) {
		return nil, errAuthorizationJSON
	}
	// Apply the limit before normalization, even without Content-Type or
	// Content-Length, and require EOF even after a complete object.
	raw, err := io.ReadAll(&jsonNULReader{source: http.MaxBytesReader(nil, r.Body, limit)})
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, tooLarge
		}
		return nil, errAuthorizationJSON
	}
	raw = bytes.Trim(raw, " \t\r\n")
	if len(raw) == 0 || raw[0] != '{' {
		return nil, errAuthorizationJSON
	}
	return raw, nil
}

func (b *authorizationBody) decode(target any, strict bool) error {
	if b.err != nil {
		return b.err
	}
	value := reflect.ValueOf(target).Elem()
	key := authorizationJSONType{target: value.Type(), strict: strict}
	zero := value.IsZero()
	if cached, ok := b.decoded[key]; ok && zero {
		value.Set(cached)
		return nil
	}
	if strict {
		decoder := json.NewDecoder(bytes.NewReader(b.raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return err
		}
		if decoder.Decode(new(json.RawMessage)) != io.EOF {
			return errAuthorizationJSON
		}
	} else if err := json.Unmarshal(b.raw, target); err != nil {
		return err
	}
	if zero {
		if b.decoded == nil {
			b.decoded = make(map[authorizationJSONType]reflect.Value)
		}
		b.decoded[key] = reflect.ValueOf(value.Interface())
	}
	return nil
}

func peekRequestJSON(r *http.Request, target any) bool {
	return peekAuthorizationJSON(r, target, false)
}

func peekStrictRequestJSON(r *http.Request, target any) bool {
	return peekAuthorizationJSON(r, target, true)
}

func peekAuthorizationJSON(r *http.Request, target any, strict bool) bool {
	defer telemetry.Begin(r.Context(), telemetry.AuthorizationBody).End()
	body := prepareAuthorizationBody(r)
	if body.err != nil {
		return false
	}
	if document := requestDocumentBody(r); document != nil {
		if document.peek(r.Context(), target) {
			return true
		}
		body.err = errAuthorizationJSON
		return false
	}
	if body.raw == nil {
		body.raw, body.err = readAuthorizationBody(r)
		if body.err != nil {
			return false
		}
	}
	if err := body.decode(target, strict); err != nil {
		// Kept as is so the answer names an unsupported field like the
		// handler's own strict decode would.
		body.err = err
		return false
	}
	return true
}

// Check this before dispatching an authorized request and before answering
// permission denials. Even advisory reads must not swallow a bad body.
func writeAuthorizationBodyError(w http.ResponseWriter, r *http.Request) bool {
	if err := authorizationBodyError(r); err != nil {
		if document := requestDocumentBody(r); document != nil && document.err != nil {
			writeDocumentBodyError(w, err)
		} else {
			requestDecodeError(w, err)
		}
		return true
	}
	return false
}
