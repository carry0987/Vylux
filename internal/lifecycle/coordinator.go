package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/internal/storage"
)

const advisoryLockNamespace int64 = 0x56594c5558
const readinessLockKey = "__vylux_media_cache_rollout_readiness__"

var ErrTombstoned = errors.New("media source is permanently tombstoned")

// HashCoordinator serializes every writer and strict cleanup for a content hash.
// Implementations must coordinate across Vylux instances, not only goroutines.
type HashCoordinator interface {
	WithHashLock(ctx context.Context, hash string, run func(*dbq.Queries) error) error
	Fence(ctx context.Context, hash, source string) error
}

// StrictReadiness exposes the persisted rollout gate used by readyz and strict
// cleanup without broadening the writer-facing HashCoordinator contract.
type StrictReadiness interface {
	AdvanceStrictCleanupReadiness(ctx context.Context) error
	RequireStrictCleanupReady(ctx context.Context) error
	RequireStrictCleanupReadyLocked(ctx context.Context, queries *dbq.Queries) error
}

// Coordinator uses a PostgreSQL session advisory lock so the lock remains held
// across the committed DB intent, Redis enqueue, object-store I/O, and cleanup.
type Coordinator struct {
	pool            *db.Pool
	readinessStore  storage.Storage
	readinessBucket string
}

func NewCoordinator(pool *db.Pool) *Coordinator {
	return &Coordinator{pool: pool}
}

// ConfigureStrictCleanupReadiness wires the bounded cache audit after storage
// construction. It must be called before serving readyz or strict cleanup.
func (c *Coordinator) ConfigureStrictCleanupReadiness(store storage.Storage, bucket string) {
	c.readinessStore = store
	c.readinessBucket = bucket
}

func (c *Coordinator) WithHashLock(ctx context.Context, hash string, run func(*dbq.Queries) error) (retErr error) {
	if c == nil || c.pool == nil {
		return fmt.Errorf("hash lifecycle coordinator is unavailable")
	}

	hash = strings.ToLower(strings.TrimSpace(hash))
	if hash == "" {
		return fmt.Errorf("hash is required")
	}

	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire lifecycle lock connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, $2))", hash, advisoryLockNamespace); err != nil {
		return fmt.Errorf("acquire lifecycle lock: %w", err)
	}

	// Register release before entering caller code so panics cannot return a
	// session-level advisory lock to the pool. A failed unlock removes and
	// closes the physical connection instead of releasing it for reuse.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, unlockErr := conn.Exec(
			unlockCtx,
			"SELECT pg_advisory_unlock(hashtextextended($1, $2))",
			hash,
			advisoryLockNamespace,
		)
		if unlockErr == nil {
			return
		}
		raw := conn.Hijack()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer closeCancel()
		_ = raw.Close(closeCtx)
		retErr = errors.Join(retErr, fmt.Errorf("release lifecycle lock: %w", unlockErr))
	}()

	return run(dbq.New(conn))
}

// Fence permanently rejects the exact host source after durable GC begins.
// It is idempotent and deliberately has no delete operation.
func (c *Coordinator) Fence(ctx context.Context, hash, source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("source is required")
	}
	hash, ok := NormalizeHash(hash)
	if !ok {
		return fmt.Errorf("hash must be a 64-character hexadecimal SHA-256")
	}
	sourceHash, ok := ExtractHash(source)
	if !ok {
		return fmt.Errorf("source does not contain an attributable content hash")
	}
	if sourceHash != hash {
		return fmt.Errorf("source content hash %s does not match hash %s", sourceHash, hash)
	}
	if c == nil || c.pool == nil {
		return fmt.Errorf("hash lifecycle coordinator is unavailable")
	}

	// Persist without waiting for the writer lock. Existing workers may already
	// be inside the lock; the strict cleaner cancels them, then takes that same
	// lock to drain and authoritatively delete every pre-fence write.
	if err := dbq.New(c.pool).CreateMediaTombstone(ctx, dbq.CreateMediaTombstoneParams{
		Hash:   hash,
		Source: source,
	}); err != nil {
		return fmt.Errorf("persist media tombstone: %w", err)
	}
	return nil
}

func RejectTombstoned(ctx context.Context, queries *dbq.Queries, hash, source string) error {
	tombstoned, err := queries.IsMediaTombstoned(ctx, dbq.IsMediaTombstonedParams{
		Hash:   strings.ToLower(strings.TrimSpace(hash)),
		Source: source,
	})
	if err != nil {
		return fmt.Errorf("check media tombstone: %w", err)
	}
	if tombstoned {
		return fmt.Errorf("%w: hash=%s source=%s", ErrTombstoned, hash, source)
	}
	return nil
}
