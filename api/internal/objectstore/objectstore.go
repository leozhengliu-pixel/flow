package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Config struct {
	Driver          string
	LocalPath       string
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	PathStyle       bool
	Prefix          string
	Validate        bool
}

type Store interface {
	Put(context.Context, string, io.Reader, string) (int64, error)
	Open(context.Context, string) (io.ReadCloser, string, int64, error)
	Delete(context.Context, string) error
}

func Open(ctx context.Context, config Config) (Store, error) {
	driver := strings.ToLower(strings.TrimSpace(config.Driver))
	if driver == "" || driver == "local" {
		root := strings.TrimSpace(config.LocalPath)
		if root == "" {
			root = "data/uploads"
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		return &localStore{root: root}, nil
	}
	if driver != "s3" {
		return nil, fmt.Errorf("unsupported storage driver %q (expected local or s3)", driver)
	}
	if strings.TrimSpace(config.Bucket) == "" || strings.TrimSpace(config.Region) == "" {
		return nil, fmt.Errorf("S3 bucket and region are required")
	}
	loadOptions := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(config.Region)}
	if config.AccessKeyID != "" || config.SecretAccessKey != "" {
		if config.AccessKeyID == "" || config.SecretAccessKey == "" {
			return nil, fmt.Errorf("S3 access key ID and secret access key must be configured together")
		}
		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, config.SessionToken)))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = config.PathStyle
		if config.Endpoint != "" {
			options.BaseEndpoint = aws.String(strings.TrimRight(config.Endpoint, "/"))
		}
	})
	store := &s3Store{client: client, bucket: config.Bucket, prefix: strings.Trim(strings.TrimSpace(config.Prefix), "/")}
	if config.Validate {
		if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(config.Bucket)}); err != nil {
			return nil, fmt.Errorf("validate S3 bucket %q: %w", config.Bucket, err)
		}
	}
	return store, nil
}

type localStore struct{ root string }

// Put reports a failure to read the caller's reader (a request body over its
// limit, a dropped upload) as it is, and a file system failure as an *Error.
func (s *localStore) Put(_ context.Context, key string, reader io.Reader, _ string) (int64, error) {
	source := &sourceReader{reader: reader}
	size, err := s.put(key, source)
	if err != nil && source.err != nil && errors.Is(err, source.err) {
		return size, err
	}
	return size, storageError("put", err)
}

// sourceReader remembers the error its reader returned.
type sourceReader struct {
	reader io.Reader
	err    error
}

func (r *sourceReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func (s *localStore) put(key string, reader io.Reader) (int64, error) {
	path, err := s.path(key)
	if err != nil {
		return 0, err
	}
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	size, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return size, copyErr
		}
		return size, closeErr
	}
	return size, nil
}

func (s *localStore) Open(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, "", 0, storageError("open", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", 0, storageError("open", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", 0, storageError("open", err)
	}
	return file, "", info.Size(), nil
}

func (s *localStore) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return storageError("delete", err)
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return storageError("delete", err)
}

func (s *localStore) path(key string) (string, error) {
	key = strings.TrimPrefix(filepath.ToSlash(key), "/")
	if key == "" || strings.Contains(key, "../") || filepath.Base(key) != key {
		return "", fmt.Errorf("invalid object key")
	}
	return filepath.Join(s.root, key), nil
}

type s3Store struct {
	client *s3.Client
	bucket string
	prefix string
}

func (s *s3Store) objectKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func (s *s3Store) Put(ctx context.Context, key string, reader io.Reader, contentType string) (int64, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.objectKey(key)), Body: bytes.NewReader(content), ContentLength: aws.Int64(int64(len(content))), ContentType: optionalString(contentType)})
	return int64(len(content)), storageError("put", err)
}

func (s *s3Store) Open(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.objectKey(key))})
	if err != nil {
		return nil, "", 0, storageError("open", err)
	}
	return result.Body, aws.ToString(result.ContentType), aws.ToInt64(result.ContentLength), nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.objectKey(key))})
	return storageError("delete", err)
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return aws.String(value)
}
