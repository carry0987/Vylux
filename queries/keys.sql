-- name: UpsertStreamEncryptionKey :one
INSERT INTO stream_encryption_keys (id, source_hash, asset_type, packaging_type, wrapped_key, wrap_nonce, kek_version, kid, scheme)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (source_hash, asset_type, packaging_type) DO UPDATE SET
	wrapped_key = EXCLUDED.wrapped_key,
	wrap_nonce = EXCLUDED.wrap_nonce,
	kek_version = EXCLUDED.kek_version,
	kid = EXCLUDED.kid,
	scheme = EXCLUDED.scheme
RETURNING *;

-- name: GetStreamEncryptionKey :one
SELECT * FROM stream_encryption_keys WHERE id = $1;

-- name: DeleteStreamEncryptionKeysBySourceHash :exec
DELETE FROM stream_encryption_keys WHERE source_hash = $1;

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
