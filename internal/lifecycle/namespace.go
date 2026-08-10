package lifecycle

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"Vylux/internal/storage"
)

var (
	exactHashPattern    = regexp.MustCompile(`(?i)^[a-f0-9]{64}$`)
	sourceHashPattern   = regexp.MustCompile(`(?i)^(?:sha256:)?([a-f0-9]{64})(?:-[^./]+)?(?:\.[^/]*)?$`)
	namespacedCachePath = regexp.MustCompile(`^cache/([a-f0-9]{2})/([a-f0-9]{64})/[a-f0-9]{64}\.[a-z0-9]+$`)
)

func NormalizeHash(hash string) (string, bool) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if !exactHashPattern.MatchString(hash) {
		return "", false
	}
	return hash, true
}

func ExtractHash(source string) (string, bool) {
	for _, segment := range strings.Split(source, "/") {
		candidate := strings.TrimSpace(segment)
		match := sourceHashPattern.FindStringSubmatch(candidate)
		if len(match) == 2 {
			return strings.ToLower(match[1]), true
		}
	}
	return "", false
}

func CacheNamespace(hash string) string {
	hash = strings.ToLower(strings.TrimSpace(hash))
	prefix := hash
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return "cache/" + prefix + "/" + hash + "/"
}

func CacheStorageKey(hash, processingHash, extension string) string {
	extension = strings.TrimPrefix(strings.ToLower(extension), ".")
	return CacheNamespace(hash) + processingHash + "." + extension
}

func CacheMemoryNamespace(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash)) + ":"
}

func CacheMemoryKey(hash, processingHash string) string {
	return CacheMemoryNamespace(hash) + processingHash
}

func IsNamespacedCacheKey(key string) bool {
	// Object-store keys are case-sensitive. Only accept the exact canonical
	// shape emitted by CacheStorageKey; normalizing here could let an uppercase
	// or path-cleanable historical key escape hash-prefix cleanup.
	cleaned := path.Clean(key)
	match := namespacedCachePath.FindStringSubmatch(key)
	return cleaned == key && len(match) == 3 && match[1] == match[2][:2]
}

type unversionedStorage interface {
	CheckUnversioned(ctx context.Context, bucket string) error
}

// CheckDeletionSemantics fails closed unless DeleteObject removes the only
// stored bytes rather than creating a version marker. Cache namespace rollout
// readiness is persisted and checked separately by Coordinator.
func CheckDeletionSemantics(ctx context.Context, store storage.Storage, bucket string) error {
	checker, ok := store.(unversionedStorage)
	if !ok {
		return fmt.Errorf("storage backend cannot prove unversioned deletion semantics")
	}
	if err := checker.CheckUnversioned(ctx, bucket); err != nil {
		return fmt.Errorf("physical deletion readiness: %w", err)
	}

	return nil
}
