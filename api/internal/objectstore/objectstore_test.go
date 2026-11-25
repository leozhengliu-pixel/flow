package objectstore

import (
	"context"
	"errors"
	"fmt"
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

// fakeMultipartS3 is an S3 endpoint that keeps one bucket's objects and
// multipart uploads in memory.
type fakeMultipartS3 struct {
	mu        sync.Mutex
	objects   map[string][]byte
	parts     map[int][]byte
	calls     []string
	failParts bool
}

func (f *fakeMultipartS3) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	query := request.URL.Query()
	key := strings.TrimPrefix(request.URL.Path, "/bucket/")
	switch {
	case request.Method == http.MethodPost && query.Has("uploads"):
		f.calls = append(f.calls, "create")
		f.parts = map[int][]byte{}
		_, _ = io.WriteString(response, `<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>`+key+`</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
	case request.Method == http.MethodPut && query.Get("uploadId") == "upload-1":
		f.calls = append(f.calls, "part")
		if f.failParts && len(f.parts) == 1 {
			response.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(response, `<Error><Code>InvalidRequest</Code><Message>failed</Message></Error>`)
			return
		}
		body, _ := io.ReadAll(request.Body)
		number := 0
		_, _ = fmt.Sscan(query.Get("partNumber"), &number)
		f.parts[number] = body
		response.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, number))
	case request.Method == http.MethodPost && query.Get("uploadId") == "upload-1":
		f.calls = append(f.calls, "complete")
		var content []byte
		for number := 1; number <= len(f.parts); number++ {
			content = append(content, f.parts[number]...)
		}
		f.objects[key] = content
		_, _ = io.WriteString(response, `<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>`+key+`</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
	case request.Method == http.MethodDelete && query.Get("uploadId") == "upload-1":
		f.calls = append(f.calls, "abort")
		f.parts = nil
		response.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodPut:
		f.calls = append(f.calls, "put")
		body, _ := io.ReadAll(request.Body)
		f.objects[key] = body
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeMultipartS3) history() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

func openFakeS3(t *testing.T, fake *fakeMultipartS3) Store {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	store, err := Open(context.Background(), Config{Driver: "s3", Bucket: "bucket", Region: "us-east-1", Endpoint: server.URL, AccessKeyID: "test-key", SecretAccessKey: "test-secret", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

var errReaderCut = errors.New("request body cut off")

// cutReader yields size bytes and then fails, like a request body cut off
// part way.
type cutReader struct{ size int }

func (r *cutReader) Read(p []byte) (int, error) {
	if r.size == 0 {
		return 0, errReaderCut
	}
	n := min(len(p), r.size)
	for index := range p[:n] {
		p[index] = 'x'
	}
	r.size -= n
	return n, nil
}

// An object larger than one part is uploaded in parts, holding one part in
// memory at a time, and a failure part way leaves no object and no
// unfinished upload behind.
func TestS3StorePutStreamsLargeObjectsInParts(t *testing.T) {
	previous := s3PartSize
	s3PartSize = 1024
	t.Cleanup(func() { s3PartSize = previous })

	fake := &fakeMultipartS3{objects: map[string][]byte{}}
	store := openFakeS3(t, fake)
	content := strings.Repeat("abcdefghij", 250) // 2500 bytes: parts of 1024, 1024 and 452
	size, err := store.Put(context.Background(), "large.bin", strings.NewReader(content), "application/octet-stream")
	if err != nil || size != 2500 {
		t.Fatalf("put size=%d err=%v", size, err)
	}
	if got := fake.history(); got != "create,part,part,part,complete" {
		t.Fatalf("calls = %s", got)
	}
	if string(fake.objects["large.bin"]) != content {
		t.Fatalf("assembled object has %d bytes", len(fake.objects["large.bin"]))
	}

	fake = &fakeMultipartS3{objects: map[string][]byte{}}
	store = openFakeS3(t, fake)
	if size, err := store.Put(context.Background(), "exact.bin", strings.NewReader(content[:1024]), ""); err != nil || size != 1024 || fake.history() != "create,part,complete" || len(fake.objects["exact.bin"]) != 1024 {
		t.Fatalf("one full part: size=%d err=%v calls=%s", size, err, fake.history())
	}
	if size, err := store.Put(context.Background(), "small.bin", strings.NewReader("tiny"), ""); err != nil || size != 4 || string(fake.objects["small.bin"]) != "tiny" {
		t.Fatalf("small object: size=%d err=%v", size, err)
	}

	fake = &fakeMultipartS3{objects: map[string][]byte{}}
	store = openFakeS3(t, fake)
	if _, err := store.Put(context.Background(), "cut.bin", &cutReader{size: 1500}, ""); !errors.Is(err, errReaderCut) || IsStorageError(err) {
		t.Fatalf("cut off body: err=%v", err)
	}
	if got := fake.history(); got != "create,part,abort" || len(fake.objects) != 0 {
		t.Fatalf("cut off body: calls=%s objects=%d", got, len(fake.objects))
	}

	fake = &fakeMultipartS3{objects: map[string][]byte{}, failParts: true}
	store = openFakeS3(t, fake)
	if _, err := store.Put(context.Background(), "failed.bin", strings.NewReader(content), ""); err == nil || !IsStorageError(err) {
		t.Fatalf("failed part: err=%v", err)
	}
	if got := fake.history(); got != "create,part,part,abort" || len(fake.objects) != 0 {
		t.Fatalf("failed part: calls=%s objects=%d", got, len(fake.objects))
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
