package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"Vylux/internal/db/dbq"
)

const cacheAuditPageSize int32 = 256

var ErrStrictReadinessIncomplete = errors.New("strict cleanup rollout readiness is incomplete")

type boundedObjectPager interface {
	ListPage(
		ctx context.Context,
		bucket, prefix, continuation string,
		limit int32,
	) (keys []string, next string, done bool, err error)
}

type cacheAuditQueries interface {
	AdvanceMediaCacheAudit(ctx context.Context, cacheAuditCursor string) error
	CompleteMediaCacheAudit(ctx context.Context) error
	FailMediaCacheAudit(ctx context.Context, cacheAuditError string) error
}

// AdvanceStrictCleanupReadiness audits at most one bounded cache page. The
// cursor and terminal state live in PostgreSQL, so restarts and multiple
// instances cannot restart an unbounded scan from the beginning.
func (c *Coordinator) AdvanceStrictCleanupReadiness(ctx context.Context) error {
	if err := c.validateReadinessConfiguration(); err != nil {
		return err
	}

	return c.WithHashLock(ctx, readinessLockKey, func(queries *dbq.Queries) error {
		state, err := queries.GetMediaLifecycleReadiness(ctx)
		if err != nil {
			return fmt.Errorf("read strict cleanup rollout state: %w", err)
		}
		if !state.CacheAuditArmed {
			return readinessStateError(false, false, "")
		}
		if state.CacheAuditComplete {
			return nil
		}
		if state.CacheAuditError != "" {
			return readinessStateError(true, false, state.CacheAuditError)
		}

		pager, ok := c.readinessStore.(boundedObjectPager)
		if !ok {
			return fmt.Errorf("strict cleanup readiness requires bounded object listing")
		}

		return auditCachePage(
			ctx,
			queries,
			pager,
			c.readinessBucket,
			state.CacheAuditCursor,
		)
	})
}

// RequireStrictCleanupReady is O(1): it reads the durable terminal marker and
// never lists cache objects. A strict cleanup therefore cannot trigger the
// rollout bucket audit on its request path.
func (c *Coordinator) RequireStrictCleanupReady(ctx context.Context) error {
	if err := c.validateReadinessConfiguration(); err != nil {
		return err
	}
	return c.RequireStrictCleanupReadyLocked(ctx, dbq.New(c.pool))
}

// RequireStrictCleanupReadyLocked reads readiness on the connection already
// holding the hash advisory lock. It avoids pool exhaustion when every
// available connection is occupied by a concurrent strict cleanup.
func (c *Coordinator) RequireStrictCleanupReadyLocked(ctx context.Context, queries *dbq.Queries) error {
	if err := c.validateReadinessConfiguration(); err != nil {
		return err
	}
	if queries == nil {
		return fmt.Errorf("strict cleanup readiness queries are unavailable")
	}

	state, err := queries.GetMediaLifecycleReadiness(ctx)
	if err != nil {
		return fmt.Errorf("read strict cleanup rollout state: %w", err)
	}
	return readinessStateError(state.CacheAuditArmed, state.CacheAuditComplete, state.CacheAuditError)
}

func (c *Coordinator) validateReadinessConfiguration() error {
	if c == nil || c.pool == nil {
		return fmt.Errorf("strict cleanup readiness database is unavailable")
	}
	if c.readinessStore == nil || strings.TrimSpace(c.readinessBucket) == "" {
		return fmt.Errorf("strict cleanup readiness storage is not configured")
	}
	return nil
}

func readinessStateError(isArmed, isComplete bool, auditError string) error {
	if !isArmed {
		return fmt.Errorf(
			"%w: cache audit is not armed; after every legacy Vylux writer is stopped, run: UPDATE media_lifecycle_readiness SET cache_audit_armed=TRUE, cache_audit_cursor='', cache_audit_complete=FALSE, cache_audit_error='', updated_at=now() WHERE singleton=TRUE",
			ErrStrictReadinessIncomplete,
		)
	}
	if isComplete {
		return nil
	}
	if auditError != "" {
		return fmt.Errorf("%w: %s", ErrStrictReadinessIncomplete, auditError)
	}
	return ErrStrictReadinessIncomplete
}

func auditCachePage(
	ctx context.Context,
	queries cacheAuditQueries,
	pager boundedObjectPager,
	bucket, cursor string,
) error {
	keys, next, done, err := pager.ListPage(ctx, bucket, "cache/", cursor, cacheAuditPageSize)
	if err != nil {
		return fmt.Errorf("audit synchronous cache namespace page: %w", err)
	}
	if len(keys) > int(cacheAuditPageSize) {
		return fmt.Errorf("bounded cache audit returned %d objects, limit is %d", len(keys), cacheAuditPageSize)
	}

	legacyExamples := make([]string, 0, 3)
	for _, key := range keys {
		if IsNamespacedCacheKey(key) {
			continue
		}
		if len(legacyExamples) < cap(legacyExamples) {
			legacyExamples = append(legacyExamples, key)
		}
	}
	if len(legacyExamples) > 0 {
		report := fmt.Sprintf(
			"legacy global cache migration required; unowned key examples=%v; migrate or delete every non-namespaced cache/ object, then run: UPDATE media_lifecycle_readiness SET cache_audit_cursor='', cache_audit_complete=FALSE, cache_audit_error='', updated_at=now() WHERE singleton=TRUE",
			legacyExamples,
		)
		if persistErr := queries.FailMediaCacheAudit(ctx, report); persistErr != nil {
			return errors.Join(
				fmt.Errorf("%w: %s", ErrStrictReadinessIncomplete, report),
				fmt.Errorf("persist cache audit failure: %w", persistErr),
			)
		}
		return fmt.Errorf("%w: %s", ErrStrictReadinessIncomplete, report)
	}

	if done {
		if err := queries.CompleteMediaCacheAudit(ctx); err != nil {
			return fmt.Errorf("persist completed cache audit: %w", err)
		}
		return nil
	}
	if next == "" || next == cursor {
		return fmt.Errorf("bounded cache audit returned an invalid continuation token")
	}
	if err := queries.AdvanceMediaCacheAudit(ctx, next); err != nil {
		return fmt.Errorf("persist cache audit continuation: %w", err)
	}
	return ErrStrictReadinessIncomplete
}
