package lifecycle

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type fakeAuditQueries struct {
	advanced string
	failed   string
	complete bool
	err      error
}

func (q *fakeAuditQueries) AdvanceMediaCacheAudit(_ context.Context, cursor string) error {
	if q.err != nil {
		return q.err
	}
	q.advanced = cursor
	return nil
}

func (q *fakeAuditQueries) CompleteMediaCacheAudit(context.Context) error {
	if q.err != nil {
		return q.err
	}
	q.complete = true
	return nil
}

func (q *fakeAuditQueries) FailMediaCacheAudit(_ context.Context, report string) error {
	q.failed = report
	return q.err
}

type fakePager struct {
	keys         []string
	next         string
	done         bool
	err          error
	calls        int
	bucket       string
	prefix       string
	continuation string
	limit        int32
}

func (p *fakePager) ListPage(
	_ context.Context,
	bucket, prefix, continuation string,
	limit int32,
) ([]string, string, bool, error) {
	p.calls++
	p.bucket = bucket
	p.prefix = prefix
	p.continuation = continuation
	p.limit = limit
	return p.keys, p.next, p.done, p.err
}

func TestAuditCachePageAdvancesOneBoundedPage(t *testing.T) {
	hash := strings.Repeat("a", 64)
	queries := &fakeAuditQueries{}
	pager := &fakePager{
		keys: []string{CacheStorageKey(hash, strings.Repeat("b", 64), "webp")},
		next: "opaque-next-token",
	}

	err := auditCachePage(context.Background(), queries, pager, "media", "old-token")
	if !errors.Is(err, ErrStrictReadinessIncomplete) {
		t.Fatalf("expected incomplete audit, got %v", err)
	}
	if pager.calls != 1 || pager.limit != cacheAuditPageSize {
		t.Fatalf("expected exactly one %d-object page, calls=%d limit=%d", cacheAuditPageSize, pager.calls, pager.limit)
	}
	if pager.bucket != "media" || pager.prefix != "cache/" || pager.continuation != "old-token" {
		t.Fatalf("unexpected page request: %#v", pager)
	}
	if queries.advanced != "opaque-next-token" || queries.complete || queries.failed != "" {
		t.Fatalf("unexpected persisted state: %#v", queries)
	}
}

func TestAuditCachePageCompletesEmptyOrNamespacedFinalPage(t *testing.T) {
	hash := strings.Repeat("c", 64)
	queries := &fakeAuditQueries{}
	pager := &fakePager{
		keys: []string{CacheStorageKey(hash, strings.Repeat("d", 64), "avif")},
		done: true,
	}

	if err := auditCachePage(context.Background(), queries, pager, "media", ""); err != nil {
		t.Fatalf("expected final page to complete audit, got %v", err)
	}
	if !queries.complete || queries.advanced != "" || queries.failed != "" {
		t.Fatalf("unexpected persisted state: %#v", queries)
	}
}

func TestAuditCachePagePersistsExplicitLegacyMigrationBlocker(t *testing.T) {
	queries := &fakeAuditQueries{}
	pager := &fakePager{
		keys: []string{
			"cache/global-processing-hash.webp",
			"cache/unowned.avif",
		},
		done: true,
	}

	err := auditCachePage(context.Background(), queries, pager, "media", "")
	if !errors.Is(err, ErrStrictReadinessIncomplete) {
		t.Fatalf("expected legacy cache blocker, got %v", err)
	}
	for _, phrase := range []string{
		"legacy global cache migration required",
		"cache/global-processing-hash.webp",
		"UPDATE media_lifecycle_readiness SET cache_audit_cursor=''",
	} {
		if !strings.Contains(queries.failed, phrase) {
			t.Fatalf("expected persisted report to contain %q, got %q", phrase, queries.failed)
		}
	}
	if queries.complete || queries.advanced != "" {
		t.Fatalf("legacy keys must not advance or complete: %#v", queries)
	}
}

func TestAuditCachePageFailsClosedWhenPagerBreaksBound(t *testing.T) {
	keys := make([]string, cacheAuditPageSize+1)
	for index := range keys {
		keys[index] = CacheStorageKey(strings.Repeat("e", 64), strings.Repeat("f", 64), "webp")
	}
	queries := &fakeAuditQueries{}
	pager := &fakePager{keys: keys, done: true}

	err := auditCachePage(context.Background(), queries, pager, "media", "")
	if err == nil || !strings.Contains(err.Error(), "returned 257 objects") {
		t.Fatalf("expected bounded-list failure, got %v", err)
	}
	if queries.complete || queries.advanced != "" || queries.failed != "" {
		t.Fatalf("invalid page must not mutate audit state: %#v", queries)
	}
}

func TestReadinessStateFailsClosedUntilDurablyComplete(t *testing.T) {
	if err := readinessStateError(false, false, ""); !errors.Is(err, ErrStrictReadinessIncomplete) ||
		!strings.Contains(err.Error(), "every legacy Vylux writer is stopped") {
		t.Fatalf("expected explicit audit arming gate, got %v", err)
	}
	if err := readinessStateError(false, true, ""); !errors.Is(err, ErrStrictReadinessIncomplete) {
		t.Fatalf("completed but unarmed state must fail closed, got %v", err)
	}
	if err := readinessStateError(true, false, ""); !errors.Is(err, ErrStrictReadinessIncomplete) {
		t.Fatalf("expected incomplete marker, got %v", err)
	}
	if err := readinessStateError(true, false, "legacy cache found"); !errors.Is(err, ErrStrictReadinessIncomplete) ||
		!strings.Contains(err.Error(), "legacy cache found") {
		t.Fatalf("expected persisted blocker, got %v", err)
	}
	if err := readinessStateError(true, true, "ignored historical error"); err != nil {
		t.Fatalf("completed audit should be ready, got %v", err)
	}
}

func TestExtractHashSupportsProductionHostStoragePaths(t *testing.T) {
	hash := strings.Repeat("A", 64)
	tests := []string{
		"uploads/" + hash + ".png",
		"tenant/media/" + hash + "-550e8400-e29b-41d4-a716-446655440000.jpeg",
		"sha256:" + hash,
		" " + hash + " ",
		" sha256:" + hash + " ",
		"uploads/" + hash + " ",
		"uploads/ " + hash + ".png",
	}

	for _, source := range tests {
		got, ok := ExtractHash(source)
		if !ok || got != strings.ToLower(hash) {
			t.Fatalf("ExtractHash(%q) = %q, %v", source, got, ok)
		}
	}
}

func TestNamespacedCacheKeyRequiresCanonicalCaseSensitivePath(t *testing.T) {
	hash := strings.Repeat("a", 64)
	processingHash := strings.Repeat("b", 64)
	canonical := CacheStorageKey(hash, processingHash, "webp")
	if !IsNamespacedCacheKey(canonical) {
		t.Fatalf("expected canonical cache key %q to be accepted", canonical)
	}

	for _, key := range []string{
		strings.ToUpper(canonical),
		canonical + "/",
		"cache/aa/" + hash + "/nested/../" + processingHash + ".webp",
		"cache/ff/" + hash + "/" + processingHash + ".webp",
	} {
		if IsNamespacedCacheKey(key) {
			t.Fatalf("non-canonical object-store key %q must block rollout readiness", key)
		}
	}
}

func TestExtractHashRejectsUnattributableOrPrefixOnlyPaths(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, source := range []string{
		"uploads/random-name.png",
		"uploads/prefix-" + hash + ".png",
		"uploads/" + hash[:32] + ".png",
	} {
		if got, ok := ExtractHash(source); ok {
			t.Fatalf("ExtractHash(%q) unexpectedly returned %q", source, got)
		}
	}
}

func TestNormalizeHashAcceptsOnlyExactSHA256(t *testing.T) {
	upper := strings.Repeat("A", 64)
	if got, ok := NormalizeHash("  " + upper + "  "); !ok || got != strings.ToLower(upper) {
		t.Fatalf("expected normalized SHA-256, got %q, %v", got, ok)
	}
	for _, value := range []string{"", "hash", upper[:63], upper + "0", "sha256:" + upper} {
		if got, ok := NormalizeHash(value); ok {
			t.Fatalf("NormalizeHash(%q) unexpectedly returned %q", value, got)
		}
	}
}

type opaqueDeletionStore struct{}

func (opaqueDeletionStore) Get(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (opaqueDeletionStore) Put(context.Context, string, string, io.Reader, string) error {
	return errors.New("not implemented")
}

func (opaqueDeletionStore) Exists(context.Context, string, string) (bool, error) {
	return false, errors.New("not implemented")
}

func (opaqueDeletionStore) Size(context.Context, string, string) (int64, error) {
	return 0, errors.New("not implemented")
}

func (opaqueDeletionStore) Delete(context.Context, string, string) error {
	return errors.New("not implemented")
}

func (opaqueDeletionStore) List(context.Context, string, string) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (opaqueDeletionStore) HeadBucket(context.Context, string) error {
	return errors.New("not implemented")
}

type versionAwareDeletionStore struct {
	opaqueDeletionStore
	err error
}

func (s versionAwareDeletionStore) CheckUnversioned(context.Context, string) error {
	return s.err
}

func TestDeletionSemanticsFailClosedWithoutUnversionedProof(t *testing.T) {
	if err := CheckDeletionSemantics(t.Context(), opaqueDeletionStore{}, "media"); err == nil ||
		!strings.Contains(err.Error(), "cannot prove unversioned deletion semantics") {
		t.Fatalf("backend without versioning capability must fail closed, got %v", err)
	}

	versioningErr := errors.New("bucket versioning status enabled retains object bytes")
	if err := CheckDeletionSemantics(t.Context(), versionAwareDeletionStore{err: versioningErr}, "media"); !errors.Is(err, versioningErr) {
		t.Fatalf("versioned backend must fail closed, got %v", err)
	}
	if err := CheckDeletionSemantics(t.Context(), versionAwareDeletionStore{}, "media"); err != nil {
		t.Fatalf("proven unversioned backend should be ready, got %v", err)
	}
}
