//go:build integration

package objectstore

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestExternalS3RoundTrip(t *testing.T) {
	endpoint := os.Getenv("FLOW_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("external S3 endpoint is not set")
	}
	store, err := Open(t.Context(), Config{
		Driver: "s3", Bucket: os.Getenv("FLOW_TEST_S3_BUCKET"), Region: "us-east-1", Endpoint: endpoint,
		AccessKeyID: os.Getenv("FLOW_TEST_S3_ACCESS_KEY"), SecretAccessKey: os.Getenv("FLOW_TEST_S3_SECRET_KEY"), PathStyle: true, Prefix: "integration", Validate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	const key, content = "round-trip.txt", "Flow S3 integration"
	size, err := store.Put(t.Context(), key, strings.NewReader(content), "text/plain")
	if err != nil || size != int64(len(content)) {
		t.Fatalf("put size=%d err=%v", size, err)
	}
	reader, contentType, loadedSize, err := store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	loaded, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil || string(loaded) != content || contentType != "text/plain" || loadedSize != size {
		t.Fatalf("open content=%q contentType=%q size=%d err=%v", loaded, contentType, loadedSize, readErr)
	}
	if err := store.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
}
