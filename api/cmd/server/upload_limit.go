package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"

	appconfig "flow/api/internal/config"
	"flow/api/internal/objectstore"
)

// The upload limit (server.storage.uploadMaxBytes in the config file, 100 MiB
// by default; there is no environment variable) is the largest file every
// upload path stores: issue, document, comment, Pulse update, customer
// request and loop attachments, Agent composer files, prepared REST and MCP
// uploads and files fetched from a URL. main sets it from the configuration;
// while it is 0 the default applies.
var uploadMaxBytes atomic.Int64

func uploadLimit() int64 {
	if limit := uploadMaxBytes.Load(); limit > 0 {
		return limit
	}
	return appconfig.DefaultUploadMaxBytes
}

// uploadLimitText is a byte limit as messages show it: "100 MB" (in MiB, as
// the app has always written its limits).
func uploadLimitText(limit int64) string {
	if limit%(1<<20) == 0 {
		return strconv.FormatInt(limit>>20, 10) + " MB"
	}
	return strconv.FormatFloat(float64(limit)/(1<<20), 'f', 1, 64) + " MB"
}

// Multipart uploads are read as a stream: the file part goes straight into
// object storage, so a request holds a few small buffers whatever the limit.
// Besides the file, a form carries only a few small fields (embed=0).
const (
	// uploadFormOverhead is what a request may carry beyond the file:
	// boundaries, part headers and the form fields.
	uploadFormOverhead = 1 << 20
	// uploadFormFieldMaxBytes and uploadFormMaxFields bound the fields.
	uploadFormFieldMaxBytes = 64 << 10
	uploadFormMaxFields     = 32
)

var (
	// errUploadForm is a request that is not a well-formed multipart form
	// (or whose fields are over their bounds).
	errUploadForm = errors.New("invalid upload form")
	// errUploadFileMissing is a form without a "file" part.
	errUploadFileMissing = errors.New("file is required")
	// errUploadTooLarge is a file over the limit.
	errUploadTooLarge = errors.New("upload is over the size limit")
	// errUploadRequest is a request body that failed while the file was being
	// stored: cut off, malformed, or over the request size bound.
	errUploadRequest = errors.New("upload request failed")
)

// uploadForm reads a multipart/form-data upload request part by part.
type uploadForm struct {
	reader *multipart.Reader
	values url.Values
	query  url.Values
	fields int
}

// openUploadForm starts reading r as a multipart form whose file may have up
// to limit bytes.
func openUploadForm(w http.ResponseWriter, r *http.Request, limit int64) (*uploadForm, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit+uploadFormOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, errUploadForm
	}
	return &uploadForm{reader: reader, values: url.Values{}, query: r.URL.Query()}, nil
}

// File advances to the file part (the first "file" part with a file name, as
// Request.FormFile finds it) and returns it unread, keeping the fields before
// it. The part's FileName is already reduced to its base name and its
// Header carries the part's Content-Type.
func (f *uploadForm) File() (*multipart.Part, error) {
	for {
		part, err := f.reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, errUploadFileMissing
		}
		if err != nil {
			return nil, errUploadForm
		}
		if part.FormName() == "file" && part.FileName() != "" {
			return part, nil
		}
		if err := f.keep(part); err != nil {
			return nil, err
		}
	}
}

// Finish reads the parts after the file, keeping their fields. Call it once
// the file is stored; an error means the request was malformed.
func (f *uploadForm) Finish() error {
	for {
		part, err := f.reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errUploadForm
		}
		if err := f.keep(part); err != nil {
			return err
		}
	}
}

// keep records a field part; other file parts are skipped (Request.FormFile
// returns only the first "file" part).
func (f *uploadForm) keep(part *multipart.Part) error {
	defer part.Close()
	if part.FileName() != "" {
		if _, err := io.Copy(io.Discard, part); err != nil {
			return errUploadForm
		}
		return nil
	}
	f.fields++
	if f.fields > uploadFormMaxFields {
		return errUploadForm
	}
	value, err := io.ReadAll(io.LimitReader(part, uploadFormFieldMaxBytes+1))
	if err != nil || len(value) > uploadFormFieldMaxBytes {
		return errUploadForm
	}
	f.values.Add(part.FormName(), string(value))
	return nil
}

// Store streams the file part (body, which may wrap it) into storage under
// key (storeUploadBody), then reads the rest of the form. If the rest is malformed
// the stored file is deleted again.
func (f *uploadForm) Store(ctx context.Context, storage objectstore.Store, key string, body io.Reader, contentType string, limit int64) (int64, error) {
	size, err := storeUploadBody(ctx, storage, key, body, contentType, limit)
	if err != nil {
		return size, err
	}
	if err := f.Finish(); err != nil {
		_ = storage.Delete(context.WithoutCancel(ctx), key)
		return size, err
	}
	return size, nil
}

// Value is Request.FormValue for the form: the URL query's value first, then
// the form field's.
func (f *uploadForm) Value(name string) string {
	if values := f.query[name]; len(values) > 0 {
		return values[0]
	}
	return f.values.Get(name)
}

// storeUploadBody streams body into storage under key, keeping at most limit
// bytes. When it fails the object is deleted again, so no partial or
// oversized file stays behind. Its errors are errUploadTooLarge,
// errUploadRequest (wrapping the request's read error) or the storage error.
func storeUploadBody(ctx context.Context, storage objectstore.Store, key string, body io.Reader, contentType string, limit int64) (int64, error) {
	size, err := storage.Put(ctx, key, io.LimitReader(body, limit+1), contentType)
	if err == nil && size <= limit {
		return size, nil
	}
	_ = storage.Delete(context.WithoutCancel(ctx), key)
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil || errors.As(err, &tooLarge):
		return size, errUploadTooLarge
	case !objectstore.IsStorageError(err):
		return size, fmt.Errorf("%w: %w", errUploadRequest, err)
	}
	return size, err
}

// writeAttachmentUploadError answers a failed attachment upload with the
// attachment endpoints' errors: 400 for a malformed form or missing file, 413
// for a file over the limit, 500 when storage failed.
func writeAttachmentUploadError(w http.ResponseWriter, err error, limit int64) {
	switch {
	case errors.Is(err, errUploadFileMissing):
		writeError(w, http.StatusBadRequest, "file is required")
	case errors.Is(err, errUploadForm), errors.Is(err, errUploadRequest):
		writeError(w, http.StatusBadRequest, "invalid attachment")
	case errors.Is(err, errUploadTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds "+uploadLimitText(limit))
	default:
		writeError(w, http.StatusInternalServerError, "upload failed")
	}
}
