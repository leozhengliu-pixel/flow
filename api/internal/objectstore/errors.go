package objectstore

import (
	"errors"
	"io/fs"
	"os"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Error is a failure of the storage backend behind a Store: an S3 (or
// S3-compatible GCS, Azure, R2, MinIO) request error or a local file system
// error. Its text names buckets, endpoints, request ids and file paths, so
// callers log it and show a generic message instead of passing it on.
type Error struct {
	Op  string
	Err error
}

func (e *Error) Error() string { return "object storage " + e.Op + ": " + e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

// storageError wraps a backend error once; nil stays nil.
func storageError(op string, err error) error {
	if err == nil {
		return nil
	}
	var wrapped *Error
	if errors.As(err, &wrapped) {
		return err
	}
	return &Error{Op: op, Err: err}
}

// IsStorageError reports an error that came from object storage, however
// deeply it was wrapped with %w: a Store error, an S3/smithy API, operation
// or HTTP response error, or a file system path error.
func IsStorageError(err error) bool {
	if err == nil {
		return false
	}
	var storeErr *Error
	var apiErr smithy.APIError
	var operationErr *smithy.OperationError
	var responseErr *smithyhttp.ResponseError
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	return errors.As(err, &storeErr) || errors.As(err, &apiErr) || errors.As(err, &operationErr) || errors.As(err, &responseErr) || errors.As(err, &pathErr) || errors.As(err, &linkErr)
}
