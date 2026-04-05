-- name: UpsertEncryptionKey :exec
INSERT INTO encryption_keys (hash, wrapped_key, wrap_nonce, kek_version, kid, scheme, key_uri)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (hash) DO UPDATE SET
	wrapped_key = EXCLUDED.wrapped_key,
	wrap_nonce = EXCLUDED.wrap_nonce,
	kek_version = EXCLUDED.kek_version,
	kid = EXCLUDED.kid,
	scheme = EXCLUDED.scheme,
	key_uri = EXCLUDED.key_uri;

-- name: GetEncryptionKey :one
SELECT * FROM encryption_keys WHERE hash = $1;

-- name: DeleteEncryptionKey :exec
DELETE FROM encryption_keys WHERE hash = $1;

-- name: UpsertImageCacheEntry :exec
INSERT INTO image_cache_entries (hash, cache_key, storage_key)
VALUES ($1, $2, $3)
ON CONFLICT (hash, storage_key) DO UPDATE SET
	cache_key = EXCLUDED.cache_key,
	created_at = now();

-- name: ListImageCacheEntriesByHash :many
SELECT * FROM image_cache_entries WHERE hash = $1 ORDER BY created_at;

-- name: DeleteImageCacheEntriesByHash :exec
DELETE FROM image_cache_entries WHERE hash = $1;
