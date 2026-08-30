package objectstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestLocalStoreLifecycle(t *testing.T) {
	store, err := Open(context.Background(), Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	size, err := store.Put(context.Background(), "attachment.txt", strings.NewReader("flow object storage"), "text/plain")
	if err != nil || size != 19 {
		t.Fatalf("put size=%d err=%v", size, err)
	}
	reader, _, readSize, err := store.Open(context.Background(), "attachment.txt")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(reader)
	reader.Close()
	if string(content) != "flow object storage" || readSize != size {
		t.Fatalf("open content=%q size=%d", content, readSize)
	}
	if err := store.Delete(context.Background(), "attachment.txt"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Open(context.Background(), "attachment.txt"); err == nil {
		t.Fatal("deleted object remained readable")
	}
}

func TestStorageConfigurationValidation(t *testing.T) {
	if _, err := Open(context.Background(), Config{Driver: "ftp"}); err == nil {
		t.Fatal("unsupported storage driver was accepted")
	}
	if _, err := Open(context.Background(), Config{Driver: "s3", Region: "us-east-1"}); err == nil {
		t.Fatal("S3 without bucket was accepted")
	}
	if _, err := Open(context.Background(), Config{Driver: "s3", Bucket: "bucket", Region: "us-east-1", AccessKeyID: "key"}); err == nil {
		t.Fatal("partial S3 credentials were accepted")
	}
}

func TestS3StoreLifecycle(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		if request.URL.Path != "/bucket/uploads/attachment.txt" {
			t.Errorf("unexpected object path %q", request.URL.Path)
		}
		switch request.Method {
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != "payload" {
				t.Errorf("unexpected upload body %q err=%v", body, err)
			}
			if request.Header.Get("Content-Type") != "text/plain" {
				t.Errorf("unexpected content type %q", request.Header.Get("Content-Type"))
			}
			response.WriteHeader(http.StatusOK)
		case http.MethodGet:
			response.Header().Set("Content-Type", "text/plain")
			response.Header().Set("Content-Length", "7")
			_, _ = response.Write([]byte("payload"))
		case http.MethodDelete:
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, err := Open(context.Background(), Config{
		Driver: "s3", Bucket: "bucket", Region: "us-east-1", Endpoint: server.URL + "/",
		AccessKeyID: "test-key", SecretAccessKey: "test-secret", PathStyle: true, Prefix: "/uploads/",
	})
	if err != nil {
		t.Fatal(err)
	}
	size, err := store.Put(context.Background(), "attachment.txt", strings.NewReader("payload"), "text/plain")
	if err != nil || size != 7 {
		t.Fatalf("put size=%d err=%v", size, err)
	}
	reader, contentType, readSize, err := store.Open(context.Background(), "attachment.txt")
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(content) != "payload" || contentType != "text/plain" || readSize != 7 {
		t.Fatalf("open content=%q type=%q size=%d readErr=%v closeErr=%v", content, contentType, readSize, readErr, closeErr)
	}
	if err := store.Delete(context.Background(), "attachment.txt"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "PUT,GET,DELETE" {
		t.Fatalf("unexpected request methods: %v", methods)
	}
}

func TestS3ObjectKeyAndOptionalString(t *testing.T) {
	if key := (&s3Store{prefix: ""}).objectKey("file.txt"); key != "file.txt" {
		t.Fatalf("unexpected unprefixed key %q", key)
	}
	if optionalString("  ") != nil || optionalString("text/plain") == nil {
		t.Fatal("optional string did not preserve meaningful values")
	}
}
