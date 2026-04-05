package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Storage implements Storage using AWS S3 / Cloudflare R2.
type S3Storage struct {
	client *s3.Client
}

// S3Config holds the parameters needed to create an S3 client.
type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
}

// NewS3 creates a new S3Storage client.
func NewS3(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true // Required for R2
	})

	return &S3Storage{client: client}, nil
}

// Get retrieves an object from S3.
func (s *S3Storage) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	return out.Body, nil
}

// Put stores an object in S3.
func (s *S3Storage) Put(ctx context.Context, bucket, key string, data io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		Body:              data,
		ContentType:       aws.String(contentType),
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc32c,
	})

	return err
}

// Exists checks whether an object exists in S3.
func (s *S3Storage) Exists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if _, ok := errors.AsType[*types.NotFound](err); ok {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// Size returns the size of an object in S3.
func (s *S3Storage) Size(ctx context.Context, bucket, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, err
	}

	return aws.ToInt64(out.ContentLength), nil
}

// Delete removes an object from S3. Idempotent — no error if key doesn't exist.
func (s *S3Storage) Delete(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	return err
}

// List returns all object keys under the given prefix.
func (s *S3Storage) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}

	return keys, nil
}

// HeadBucket checks connectivity to a bucket (used by readiness probe).
func (s *S3Storage) HeadBucket(ctx context.Context, bucket string) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		slog.Warn("S3 HeadBucket failed", "bucket", bucket, "error", err)
	}

	return err
}
