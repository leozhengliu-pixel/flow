package objectstore

import (
	"context"
	"io"
	"strings"
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
}
