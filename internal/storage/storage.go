package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Storage defines the interface for object storage operations.
type Storage interface {
	// Get retrieves an object by key and returns its contents as a ReadCloser.
	// The caller is responsible for closing the reader.
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, error)

	// Put stores data under the given key. contentType specifies the MIME type.
	Put(ctx context.Context, bucket, key string, data io.Reader, contentType string) error

	// Exists checks whether an object exists at the given key.
	Exists(ctx context.Context, bucket, key string) (bool, error)

	// Size returns the object size in bytes.
	Size(ctx context.Context, bucket, key string) (int64, error)

	// Delete removes an object by key. Returns nil if the key does not exist (idempotent).
	Delete(ctx context.Context, bucket, key string) error

	// List returns all object keys under the given prefix.
	List(ctx context.Context, bucket, prefix string) ([]string, error)

	// HeadBucket checks connectivity to a bucket (used by readiness probe).
	HeadBucket(ctx context.Context, bucket string) error
}

type errorCoder interface {
	error
	ErrorCode() string
}

// IsNotFound reports whether the storage error indicates a missing object.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}

	if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
		return true
	}

	if _, ok := errors.AsType[*types.NotFound](err); ok {
		return true
	}

	if apiErr, ok := errors.AsType[errorCoder](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}

	return false
}
