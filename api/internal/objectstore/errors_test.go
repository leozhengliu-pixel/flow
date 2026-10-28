package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// Backend failures are typed storage errors through any %w wrapping, keep
// errors.Is(err, fs.ErrNotExist), and the caller's own reader failure is not
// one.
func TestStorageErrorsAreRecognised(t *testing.T) {
	ctx := context.Background()
	local, err := Open(ctx, Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, openErr := local.Open(ctx, "missing.txt")
	if !IsStorageError(openErr) || !IsStorageError(fmt.Errorf("load attachment: %w", openErr)) || !errors.Is(openErr, fs.ErrNotExist) {
		t.Fatalf("local open error = %#v", openErr)
	}
	if _, err := local.Put(ctx, "../escape", strings.NewReader("x"), ""); !IsStorageError(err) {
		t.Fatalf("invalid key error = %v", err)
	}
	bodyErr := errors.New("http: request body too large")
	if _, err := local.Put(ctx, "body.txt", failingReader{bodyErr}, ""); !errors.Is(err, bodyErr) || IsStorageError(err) {
		t.Fatalf("reader error = %v (storage %v)", err, IsStorageError(err))
	}
	if IsStorageError(nil) || IsStorageError(errors.New("title is required")) {
		t.Fatal("a plain error is a storage error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message><RequestId>REQ123</RequestId></Error>`))
	}))
	defer server.Close()
	remote, err := Open(ctx, Config{Driver: "s3", Bucket: "private-bucket", Region: "us-east-1", Endpoint: server.URL, AccessKeyID: "key", SecretAccessKey: "secret", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	_, putErr := remote.Put(ctx, "object.txt", strings.NewReader("payload"), "text/plain")
	deleteErr := remote.Delete(ctx, "object.txt")
	for _, err := range []error{putErr, deleteErr, fmt.Errorf("store offloaded content: %w", putErr)} {
		if !IsStorageError(err) || !strings.Contains(err.Error(), "AccessDenied") {
			t.Fatalf("S3 error = %v", err)
		}
	}
}
