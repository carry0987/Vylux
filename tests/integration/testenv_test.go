package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"Vylux/internal/cache"
	"Vylux/internal/config"
	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/internal/storage"
	"Vylux/migrations"
	"Vylux/tests/testutil"

	redis "github.com/redis/go-redis/v9"
)

type sharedIntegrationEnv struct {
	mu         sync.Mutex
	pg         *testutil.PostgresContainer
	rd         *testutil.RedisContainer
	rs         *testutil.RustFSContainer
	pool       *db.Pool
	rawStore   storage.Storage
	redisAdmin *redis.Client
	baseConfig config.Config
}

var integrationEnv *sharedIntegrationEnv

func TestMain(m *testing.M) {
	ctx := context.Background()
	env, err := newSharedIntegrationEnv(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup integration env: %v\n", err)
		os.Exit(1)
	}
	integrationEnv = env

	exitCode := m.Run()
	if err := env.close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "teardown integration env: %v\n", err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func newSharedIntegrationEnv(ctx context.Context) (*sharedIntegrationEnv, error) {
	pg, err := testutil.StartPostgresContainer(ctx)
	if err != nil {
		return nil, err
	}
	rd, err := testutil.StartRedisContainer(ctx)
	if err != nil {
		_ = pg.Terminate(ctx)
		return nil, err
	}
	rs, err := testutil.StartRustFSContainer(ctx)
	if err != nil {
		_ = rd.Terminate(ctx)
		_ = pg.Terminate(ctx)
		return nil, err
	}
	if err := testutil.CreateBuckets(ctx, rs.Endpoint, rs.AccessKey, rs.SecretKey, rs.Region, "source", "media"); err != nil {
		_ = rs.Terminate(ctx)
		_ = rd.Terminate(ctx)
		_ = pg.Terminate(ctx)
		return nil, fmt.Errorf("create rustfs buckets: %w", err)
	}
	if err := db.Migrate(ctx, pg.DSN, migrations.FS); err != nil {
		_ = rs.Terminate(ctx)
		_ = rd.Terminate(ctx)
		_ = pg.Terminate(ctx)
		return nil, fmt.Errorf("migrate: %w", err)
	}
	pool, err := db.Connect(ctx, pg.DSN)
	if err != nil {
		_ = rs.Terminate(ctx)
		_ = rd.Terminate(ctx)
		_ = pg.Terminate(ctx)
		return nil, fmt.Errorf("connect db: %w", err)
	}
	store, err := storage.NewS3(ctx, storage.S3Config{
		Endpoint:  rs.Endpoint,
		AccessKey: rs.AccessKey,
		SecretKey: rs.SecretKey,
		Region:    rs.Region,
	})
	if err != nil {
		pool.Close()
		_ = rs.Terminate(ctx)
		_ = rd.Terminate(ctx)
		_ = pg.Terminate(ctx)
		return nil, fmt.Errorf("new s3 store: %w", err)
	}
	redisOpt, err := redis.ParseURL(rd.URL)
	if err != nil {
		pool.Close()
		_ = rs.Terminate(ctx)
		_ = rd.Terminate(ctx)
		_ = pg.Terminate(ctx)
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	redisAdmin := redis.NewClient(redisOpt)

	return &sharedIntegrationEnv{
		pg:         pg,
		rd:         rd,
		rs:         rs,
		pool:       pool,
		rawStore:   store,
		redisAdmin: redisAdmin,
		baseConfig: config.Config{
			Port:               3000,
			Mode:               "server",
			BaseURL:            "http://localhost:3000",
			DatabaseURL:        pg.DSN,
			RedisURL:           rd.URL,
			SourceS3Endpoint:   rs.Endpoint,
			SourceS3AccessKey:  rs.AccessKey,
			SourceS3SecretKey:  rs.SecretKey,
			SourceS3Region:     rs.Region,
			SourceBucket:       "source",
			MediaS3Endpoint:    rs.Endpoint,
			MediaS3AccessKey:   rs.AccessKey,
			MediaS3SecretKey:   rs.SecretKey,
			MediaS3Region:      rs.Region,
			MediaBucket:        "media",
			HMACSecret:         "test-hmac-secret",
			APIKey:             "test-api-key",
			WebhookSecret:      "test-webhook-secret",
			KeyTokenSecret:     "test-key-token-secret",
			EncryptionKey:      "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
			LargeFileThreshold: 5 * 1024 * 1024 * 1024,
			CacheMaxSize:       64 * 1024 * 1024,
		},
	}, nil
}

func (e *sharedIntegrationEnv) close(ctx context.Context) error {
	var firstErr error
	setFirstErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if e.redisAdmin != nil {
		setFirstErr(e.redisAdmin.Close())
	}
	if e.pool != nil {
		e.pool.Close()
	}
	if e.rs != nil {
		setFirstErr(e.rs.Terminate(ctx))
	}
	if e.rd != nil {
		setFirstErr(e.rd.Terminate(ctx))
	}
	if e.pg != nil {
		setFirstErr(e.pg.Terminate(ctx))
	}
	return firstErr
}

func (e *sharedIntegrationEnv) reset(ctx context.Context) error {
	if err := e.redisAdmin.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("flush redis: %w", err)
	}
	if _, err := e.pool.Exec(ctx, "TRUNCATE jobs, image_cache_entries, stream_encryption_keys"); err != nil {
		return fmt.Errorf("truncate tables: %w", err)
	}
	for _, bucket := range []string{e.baseConfig.SourceBucket, e.baseConfig.MediaBucket} {
		keys, err := e.rawStore.List(ctx, bucket, "")
		if err != nil {
			return fmt.Errorf("list bucket %s: %w", bucket, err)
		}
		for _, key := range keys {
			if err := e.rawStore.Delete(ctx, bucket, key); err != nil {
				return fmt.Errorf("delete %s/%s: %w", bucket, key, err)
			}
		}
	}
	return nil
}

func cloneIntegrationConfig() *config.Config {
	cfg := integrationEnv.baseConfig
	return &cfg
}

func newIntegrationLRU() *cache.LRU {
	return cache.New(64 * 1024 * 1024)
}

func newIntegrationQueries() *dbq.Queries {
	return dbq.New(integrationEnv.pool)
}
