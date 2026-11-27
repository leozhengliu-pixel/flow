package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The streaming document body decoder answers what decodeJSON answered for
// the same body: the same input, or an error for the same bodies.
func TestDocumentBodyDecodesLikeEncodingJSON(t *testing.T) {
	random := rand.New(rand.NewSource(264))
	alphabet := []rune("abc xyz \"\\/<>&\n\t\r\b\f é文档😀\x01")
	var long strings.Builder
	for long.Len() < 300<<10 {
		long.WriteRune(alphabet[random.Intn(len(alphabet))])
	}
	longJSON, err := json.Marshal(map[string]any{"title": "Long", "content": long.String(), "contentState": strings.Repeat("AAEC", 40<<10), "contentData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "text", "text": long.String()[:70<<10]}}}})
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{
		`{}`, ` { } `, `null`, `{"title":"a","content":"plain"}`, string(longJSON),
		`{"content":"esc \" \\ \/ \b \f \n \r \t é 文 😀 😀 end"}`,
		`{"content":"\ud83d x \ude00 y \ud83dA \ud83d😀 \udfff"}`,
		"{\"content\":\"a\xff\xfeb\xe6\x96\xed\xa0\x80c\"}",
		`{"content":"a\u0000b","title":"x\u0000","contentState":"\u0000"}`,
		`{"content":null,"contentState":"state","contentData":{"type":"doc","content":[{"type":"text","text":"x \"}\" ]"}]},"projectIds":["a","b"],"favorite":true,"expectedVersion":3}`,
		`{"unknown":{"nested":[1,2,{"a":"}"}]},"content":"x","documentUpdateIds":[]}`,
		"{\n\t\"expectedVersion\" : 12 ,\r\n \"content\" : \"x\" ,\"archived\":false}",
		`{"content":"x"} trailing`, `{"Content":"x"}`, `{"contentData":null}`, `{"teamIds":null,"issueId":""}`,
		`{"content":"x"`, `{"content":"x",}`, `{"content":x}`, "{\"content\":\"a\x01b\"}", `{"content":"\q"}`,
		`{"content":"\u12G4"}`, `{"content":"\u12"}`, `[`, ``, `   `, `{"title":1}`, `{"contentData":[1]}`, `{"content":5}`,
		`{,}`, `{"a" "b"}`, `{"a":}`, `{"a":1 "b":2}`, `{"a":[1}`, `[1]`, `"text"`, `{"content":"x"]`,
	}
	for _, body := range bodies {
		var want documentInput
		wantErr := json.NewDecoder(&jsonNULReader{source: strings.NewReader(body)}).Decode(&want)
		var got documentInput
		fields, gotErr := decodeDocumentObject(&jsonNULReader{source: strings.NewReader(body)}, &got)
		label := body
		if len(label) > 80 {
			label = label[:80]
		}
		if (wantErr != nil) != (gotErr != nil) {
			t.Fatalf("%q: error %v, encoding/json %v", label, gotErr, wantErr)
		}
		if wantErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: decoded %+v, encoding/json %+v", label, got, want)
		}
		if gotErr == nil && bytes.Contains(fields, []byte(`"content":"`)) {
			t.Fatalf("%q: the small members carry the content", label)
		}
	}
	// A member that is decoded on its own may not be named twice.
	for _, body := range []string{`{"content":"a","content":"b"}`, `{"contentState":"a","contentState":null}`, `{"contentData":{},"contentData":{}}`} {
		var input documentInput
		if _, err := decodeDocumentObject(strings.NewReader(body), &input); err == nil {
			t.Fatalf("%s accepted", body)
		}
	}
}

// Document create and update take bodies up to the document limit, decoded
// once for the authorization peeks and the handler; other routes keep the
// general 16 MiB.
func TestDocumentRequestBodyLimits(t *testing.T) {
	defer documentBodyMaxBytes.Store(documentBodyMaxBytes.Load())
	documentBodyMaxBytes.Store(0)
	s := &server{}
	if got := s.documentBodyLimit(); got != maxJSONRequestBytes {
		t.Fatalf("automatic limit without a store = %d, want 16 MiB", got)
	}
	const limit = 40 << 20
	documentBodyMaxBytes.Store(limit)

	var decoded, peeked documentInput
	var attribution oauthAttributionInput
	reached := 0
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		reached++
		// As authorizeResourceRequest, authorization peeks and OAuth
		// attribution read the body before the handler.
		if !decodeRequestDocumentBody(w, r) {
			return
		}
		if !peekRequestJSON(r, &peeked) || !peekRequestJSON(r, &attribution) {
			writeError(w, http.StatusForbidden, "peek failed")
			return
		}
		if s.decodeDocumentBody(w, r, &decoded) {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("PATCH /api/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		reached++
		var input map[string]any
		if decodeJSON(w, r, &input) {
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /api/documents/{id}/comments", func(w http.ResponseWriter, r *http.Request) { reached++ })
	handler := requestBodyBudget(s.documentRequestBodies(mux))
	send := func(method, path string, body []byte, length int64) *httptest.ResponseRecorder {
		t.Helper()
		reached = 0
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = length
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// 17 MiB of text, past the general limit, round-trips byte for byte.
	content := strings.Repeat("large \"document\" text é 文档 😀\n", (17<<20)/40)
	body, err := json.Marshal(map[string]any{"content": content, "title": "Large", "createAsUser": "someone"})
	if err != nil {
		t.Fatal(err)
	}
	if w := send(http.MethodPatch, "/api/documents/doc", body, int64(len(body))); w.Code != http.StatusNoContent {
		t.Fatalf("large document body: %d %s", w.Code, w.Body)
	}
	if decoded.Content == nil || *decoded.Content != content || peeked.Content != decoded.Content || decoded.Title == nil || *decoded.Title != "Large" {
		t.Fatal("the large document body did not round-trip")
	}
	if attribution.CreateAsUser == nil || *attribution.CreateAsUser != "someone" {
		t.Fatal("a peek into another type lost the small members")
	}

	// Other routes, document sub-routes included, keep 16 MiB.
	if w := send(http.MethodPatch, "/api/issues/issue", body, int64(len(body))); w.Code != http.StatusRequestEntityTooLarge || reached != 0 {
		t.Fatalf("an issue body over 16 MiB: %d reached=%d", w.Code, reached)
	}
	if w := send(http.MethodPost, "/api/documents/doc/comments", body, int64(len(body))); w.Code != http.StatusRequestEntityTooLarge || reached != 0 {
		t.Fatalf("a document comment body over 16 MiB: %d reached=%d", w.Code, reached)
	}
	if w := send(http.MethodPatch, "/api/issues/issue", body, -1); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an issue body over 16 MiB without Content-Length: %d", w.Code)
	}

	// Over the document limit: 413 before the body is read.
	w := send(http.MethodPatch, "/api/documents/doc", []byte(`{}`), limit+1)
	var refusal struct{ Error string }
	if w.Code != http.StatusRequestEntityTooLarge || reached != 0 || json.Unmarshal(w.Body.Bytes(), &refusal) != nil || refusal.Error != documentBodyTooLargeMessage {
		t.Fatalf("a document body over the limit: %d reached=%d %s", w.Code, reached, w.Body)
	}
	// Without Content-Length a document body keeps 16 MiB.
	if w := send(http.MethodPatch, "/api/documents/doc", body, -1); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a document body over 16 MiB without Content-Length: %d %s", w.Code, w.Body)
	}
	// Invalid JSON is 400.
	if w := send(http.MethodPatch, "/api/documents/doc", []byte(`{"content":`), 11); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid document JSON: %d", w.Code)
	}
}

// The handler decodes a document body that no middleware prepared (the
// in-process MCP path before its request is authorized, tests).
func TestDocumentBodyWithoutMiddleware(t *testing.T) {
	s := &server{}
	r := httptest.NewRequest(http.MethodPatch, "/api/documents/doc", io.NopCloser(strings.NewReader(`{"content":"xé"}`)))
	w := httptest.NewRecorder()
	var input documentInput
	if !s.decodeDocumentBody(w, r, &input) || input.Content == nil || *input.Content != "xé" {
		t.Fatalf("decode without middleware: %d %+v", w.Code, input)
	}
}

// Authorization reads a document body's ids before the handler. The general
// peek keeps only the first megabyte of a body (and cuts the body there), so
// a signed-in edit over 1 MiB was refused; a document body is instead decoded
// once and shared by the peeks and the handler.
func TestDocumentBodyPeekKeepsLargeBodies(t *testing.T) {
	content := strings.Repeat("text over one megabyte ", (2<<20)/23)
	raw, err := json.Marshal(map[string]any{"content": content, "projectIds": []string{"project"}})
	if err != nil {
		t.Fatal(err)
	}
	var input documentInput
	plain := httptest.NewRequest(http.MethodPatch, "/api/documents/doc", bytes.NewReader(raw))
	if peekRequestJSON(plain, &input) {
		t.Fatal("the general peek read a body over 1 MiB")
	}
	if rest, _ := io.ReadAll(plain.Body); len(rest) != 1<<20 {
		t.Fatalf("the general peek left %d bytes of the body", len(rest))
	}

	s := &server{}
	shared := withDocumentRequestBody(s, httptest.NewRecorder(), httptest.NewRequest(http.MethodPatch, "/api/documents/doc", bytes.NewReader(raw)))
	if !peekRequestJSON(shared, &input) || input.ProjectIDs == nil || (*input.ProjectIDs)[0] != "project" {
		t.Fatal("the document peek lost the body's ids")
	}
	var decoded documentInput
	if !s.decodeDocumentBody(httptest.NewRecorder(), shared, &decoded) || decoded.Content == nil || *decoded.Content != content {
		t.Fatal("the handler did not get the whole document body after a peek")
	}
}
