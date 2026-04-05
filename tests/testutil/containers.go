package testutil

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer wraps a testcontainers PostgreSQL instance.
type PostgresContainer struct {
	*postgres.PostgresContainer
	DSN string
}

// StartPostgres launches a PostgreSQL container for integration testing.
func StartPostgres(ctx context.Context, t *testing.T) *PostgresContainer {
	t.Helper()

	ctr, err := postgres.Run(ctx,
		"postgres:18.1-alpine",
		postgres.WithDatabase("media_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("terminate postgres: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get postgres DSN: %v", err)
	}

	return &PostgresContainer{PostgresContainer: ctr, DSN: dsn}
}

// RedisContainer wraps a testcontainers Redis instance.
type RedisContainer struct {
	*redis.RedisContainer
	URL string
}

// StartRedis launches a Redis container for integration testing.
func StartRedis(ctx context.Context, t *testing.T) *RedisContainer {
	t.Helper()

	ctr, err := redis.Run(ctx,
		"redis:8.4-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(15*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}

	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("terminate redis: %v", err)
		}
	})

	ep, err := ctr.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("get redis endpoint: %v", err)
	}

	return &RedisContainer{
		RedisContainer: ctr,
		URL:            fmt.Sprintf("redis://%s", ep),
	}
}

// RustFSContainer wraps a RustFS S3-compatible object storage instance.
type RustFSContainer struct {
	testcontainers.Container
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
}

// StartRustFS launches a RustFS container for integration testing.
func StartRustFS(ctx context.Context, t *testing.T) *RustFSContainer {
	t.Helper()

	const (
		accessKey = "test-access-key"
		secretKey = "test-secret-key"
		region    = "us-east-1"
	)

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rustfs/rustfs:latest",
			ExposedPorts: []string{"9000/tcp"},
			Env: map[string]string{
				"RUSTFS_ACCESS_KEY": accessKey,
				"RUSTFS_SECRET_KEY": secretKey,
			},
			WaitingFor: wait.ForHTTP("/health").
				WithPort("9000/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(45 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start rustfs container: %v", err)
	}

	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("terminate rustfs: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("get rustfs host: %v", err)
	}

	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("get rustfs port: %v", err)
	}

	return &RustFSContainer{
		Container: ctr,
		Endpoint:  fmt.Sprintf("http://%s:%s", host, port.Port()),
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    region,
	}
}

// CreateBuckets creates the named buckets in a test S3-compatible object store.
func CreateBuckets(ctx context.Context, endpoint, accessKey, secretKey, region string, buckets ...string) error {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		),
	)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	for _, bucket := range buckets {
		if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
			return fmt.Errorf("create bucket %s: %w", bucket, err)
		}
		if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
			return fmt.Errorf("head bucket %s: %w", bucket, err)
		}
	}

	return nil
}
