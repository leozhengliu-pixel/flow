package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
)

func first(response *http.Response, _ []byte) *http.Response { return response }

// uploadFile posts a file to the issue's attachments with the declared media type.
func (e *surfaceEnv) uploadFile(filename, mediaType string, content []byte) domain.Attachment {
	e.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	if mediaType != "" {
		partHeader.Set("Content-Type", mediaType)
	}
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = part.Write(content)
	_ = writer.Close()
	response, raw := e.admins(http.MethodPost, "/api/issues/"+e.issueID+"/attachments", body.Bytes(), http.Header{"Content-Type": {writer.FormDataContentType()}})
	e.expect("upload "+filename, response, http.StatusCreated, kindAPI)
	var attachment domain.Attachment
	if err := json.Unmarshal(raw, &attachment); err != nil {
		e.t.Fatalf("upload %s: %v: %s", filename, err, raw)
	}
	return attachment
}

// declaredTypeStore behaves like the S3 store: the media type a client
// declared at upload is the one the object is served with (the local store
// derives it from the file name instead).
type declaredTypeStore struct {
	objectstore.Store
	mu    sync.Mutex
	types map[string]string
}

func (s *declaredTypeStore) Put(ctx context.Context, key string, reader io.Reader, contentType string) (int64, error) {
	s.mu.Lock()
	s.types[key] = contentType
	s.mu.Unlock()
	return s.Store.Put(ctx, key, reader, contentType)
}

func (s *declaredTypeStore) Open(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	reader, _, size, err := s.Store.Open(ctx, key)
	s.mu.Lock()
	defer s.mu.Unlock()
	return reader, s.types[key], size, err
}

var onePixelPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa7\x9a\xa0\xa0\x00\x00\x00\x00IEND\xaeB`\x82")

// inlineMediaTypes are the only types a browser may show in place. Anything
// else, HTML and SVG above all, is a download.
func shownInPlace(mediaType string) bool {
	parsed, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return false
	}
	switch parsed {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "application/pdf":
		return true
	}
	return strings.HasPrefix(parsed, "audio/") || strings.HasPrefix(parsed, "video/")
}

type uploadCase struct {
	name, mediaType string
	content         []byte
	// local is whether the local store (media type from the file name) shows
	// it in place; "" means the case only applies to a store that keeps the
	// declared type.
	local string
}

var uploadCases = []uploadCase{
	{"photo.png", "image/png", onePixelPNG, "inline"},
	{"clip.mp4", "video/mp4", []byte("\x00\x00\x00\x18ftypmp42"), ""},
	{"clip.webm", "video/webm", []byte("\x1aE\xdf\xa3"), ""},
	{"voice.mp3", "audio/mpeg", []byte("ID3"), ""},
	{"doc.pdf", "application/pdf", []byte("%PDF-1.4\n%%EOF"), "inline"},
	{"page.html", "text/html", []byte("<script>fetch('/api/bootstrap')</script>"), "download"},
	{"image.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script></svg>`), "download"},
	{"page.xhtml", "application/xhtml+xml", []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></html>`), "download"},
	{"data.xml", "text/xml", []byte(`<x xmlns:h="http://www.w3.org/1999/xhtml"><h:script>alert(1)</h:script></x>`), "download"},
	{"liar.png", "text/html", []byte("<script>alert(1)</script>"), "inline"},
	{"sheet.css", "text/css", []byte("body{}"), "download"},
	{"script.js", "text/javascript", []byte("alert(1)"), "download"},
	{"module.mjs", "application/javascript", []byte("alert(1)"), "download"},
	{"plain.bin", "", []byte("<html><script>alert(1)</script></html>"), "download"},
	{"noext", "application/octet-stream", []byte("<html><script>alert(1)</script></html>"), "download"},
	// A client can declare any media type, including ones that are not media types.
	{"odd1.dat", "image/png-evil", []byte("<svg onload=alert(1)>"), ""},
	{"odd2.dat", "IMAGE/SVG+XML", []byte("<svg onload=alert(1)>"), ""},
	{"odd3.dat", "text/html;image/png", []byte("<svg onload=alert(1)>"), ""},
	{"odd4.dat", "image/svg+xml;image/png", []byte("<svg onload=alert(1)>"), ""},
	{"odd5.dat", "image/pngx", []byte("<svg onload=alert(1)>"), ""},
	{"odd6.dat", "application/pdfx", []byte("<svg onload=alert(1)>"), ""},
}

// Attachments: uploads, previews and downloads. Whatever a user uploads is
// served from the app origin, so it must never run there: scripts and styles
// are off (sandbox policy), the type is never sniffed, and anything a browser
// could render as a document is a download. Run against the local store
// (type from the file name) and against a store that keeps the declared type.
func TestSecuritySurfaceAttachments(t *testing.T) {
	for _, declared := range []bool{false, true} {
		name := "local store"
		if declared {
			name = "declared types"
		}
		t.Run(name, func(t *testing.T) {
			forEachSurfaceMode(t, func(s *server, _ *appconfig.SecurityHeadersConfig) {
				if declared {
					local, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: s.uploadPath})
					if err != nil {
						t.Fatal(err)
					}
					s.objectStore = &declaredTypeStore{Store: local, types: map[string]string{}}
				}
			}, func(t *testing.T, e *surfaceEnv) {
				for _, item := range uploadCases {
					wantInline := shownInPlace(item.mediaType)
					if !declared {
						if item.local == "" {
							continue
						}
						wantInline = item.local == "inline"
					}
					attachment := e.uploadFile(item.name, item.mediaType, item.content)
					// The signed link a client hands to a viewer without a session.
					signed, raw := e.admins(http.MethodGet, "/api/files/sign?url="+url.QueryEscape(attachment.URL), nil, nil)
					e.expect("sign "+item.name, signed, http.StatusOK, kindAPI)
					var signedBody struct {
						Files []struct {
							SignedURL string `json:"signedUrl"`
						} `json:"files"`
					}
					if err := json.Unmarshal(raw, &signedBody); err != nil || len(signedBody.Files) != 1 {
						t.Fatalf("sign %s: %v %s", item.name, err, raw)
					}
					signedURL, err := url.Parse(signedBody.Files[0].SignedURL)
					if err != nil {
						t.Fatal(err)
					}
					responses := map[string]*http.Response{
						"preview /uploads":  first(e.admins(http.MethodGet, attachment.URL, nil, nil)),
						"HEAD /uploads":     first(e.admins(http.MethodHead, attachment.URL, nil, nil)),
						"signed download":   first(e.do(surfaceRequest{path: signedURL.RequestURI()})),
						"signed with Range": first(e.do(surfaceRequest{path: signedURL.RequestURI(), header: http.Header{"Range": {"bytes=0-1"}}})),
					}
					for label, response := range responses {
						label += " " + item.name
						if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
							t.Errorf("%s [%s]: status %d", label, e.mode, response.StatusCode)
							continue
						}
						e.checkHeaders(label, response, kindFile)
						disposition, contentType := response.Header.Get("Content-Disposition"), response.Header.Get("Content-Type")
						download := strings.HasPrefix(disposition, "attachment")
						if wantInline && download {
							t.Errorf("%s [%s]: a %s preview is a download (%q), the browser would not show it", label, e.mode, item.name, disposition)
						}
						if !wantInline && !download {
							t.Errorf("%s [%s]: %s (declared %q, served as %q) is shown in place: Content-Disposition %q", label, e.mode, item.name, item.mediaType, contentType, disposition)
						}
					}
				}
				// Errors: a missing file, a path walk, a bad or expired signature, an unreadable file.
				response, _ := e.admins(http.MethodGet, "/uploads/missing.png", nil, nil)
				e.expect("missing upload", response, http.StatusNotFound, kindAPI)
				response, _ = e.admins(http.MethodGet, "/uploads/..%2f..%2fflow.db", nil, nil)
				e.checkHeaders("upload path walk", response, kindAPI)
				if response.StatusCode == http.StatusOK {
					t.Errorf("upload path walk: %d", response.StatusCode)
				}
				response, _ = e.do(surfaceRequest{path: "/uploads/missing.png"})
				e.expect("upload without a session", response, http.StatusUnauthorized, kindAPI)
				response, _ = e.do(surfaceRequest{path: "/api/files/signed/photo.png?expires=9999999999&signature=00"})
				e.expect("signed download, bad signature", response, http.StatusForbidden, kindAPI)
				response, _ = e.do(surfaceRequest{path: "/api/files/signed/photo.png?expires=1&signature=00"})
				e.expect("signed download, expired", response, http.StatusForbidden, kindAPI)
				response, _ = e.do(surfaceRequest{path: "/api/files/signed/photo.png"})
				e.expect("signed download, no signature", response, http.StatusNotFound, kindAPI)
				response, _ = e.admins(http.MethodPost, "/api/issues/"+e.issueID+"/attachments", []byte("not multipart"), http.Header{"Content-Type": {"text/plain"}})
				e.expect("upload that is not multipart", response, http.StatusBadRequest, kindAPI)
				response, _ = e.do(surfaceRequest{method: http.MethodPost, path: "/api/issues/" + e.issueID + "/attachments", body: []byte("x"), header: http.Header{"Content-Type": {"text/plain"}}})
				e.expect("upload without a session", response, http.StatusUnauthorized, kindAPI)
			})
		})
	}
}
