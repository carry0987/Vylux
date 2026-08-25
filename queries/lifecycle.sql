-- name: CreateMediaTombstone :exec
INSERT INTO media_tombstones (hash, source)
VALUES ($1, $2)
ON CONFLICT (hash, source) DO NOTHING;

-- name: IsMediaTombstoned :one
SELECT EXISTS (
    SELECT 1 FROM media_tombstones WHERE hash = $1 AND source = $2
);

-- name: GetMediaLifecycleReadiness :one
SELECT singleton, cache_audit_armed, cache_audit_cursor, cache_audit_complete, cache_audit_error, updated_at
FROM media_lifecycle_readiness
WHERE singleton = TRUE;

-- name: BindMediaDeploymentTarget :execrows
UPDATE media_lifecycle_readiness
SET protocol_version = sqlc.arg(protocol_version),
    deployment_id = sqlc.arg(deployment_id),
    source_backend_identity = sqlc.arg(source_backend_identity),
    media_backend_identity = sqlc.arg(media_backend_identity),
    updated_at = now()
WHERE singleton = TRUE
  AND protocol_version IS NULL
  AND deployment_id IS NULL
  AND source_backend_identity IS NULL
  AND media_backend_identity IS NULL;

-- name: GetMediaDeploymentTarget :one
SELECT COALESCE(protocol_version, 0)::smallint AS protocol_version,
       COALESCE(deployment_id, '')::text AS deployment_id,
       COALESCE(source_backend_identity, '')::text AS source_backend_identity,
       COALESCE(media_backend_identity, '')::text AS media_backend_identity
FROM media_lifecycle_readiness
WHERE singleton = TRUE;

-- name: AdvanceMediaCacheAudit :exec
UPDATE media_lifecycle_readiness
SET cache_audit_cursor = $1,
    updated_at = now()
WHERE singleton = TRUE
  AND cache_audit_complete = FALSE
  AND cache_audit_armed = TRUE
  AND cache_audit_error = '';

-- name: CompleteMediaCacheAudit :exec
UPDATE media_lifecycle_readiness
SET cache_audit_complete = TRUE,
    cache_audit_error = '',
    updated_at = now()
WHERE singleton = TRUE
  AND cache_audit_armed = TRUE;

-- name: FailMediaCacheAudit :exec
UPDATE media_lifecycle_readiness
SET cache_audit_error = $1,
    updated_at = now()
WHERE singleton = TRUE
  AND cache_audit_armed = TRUE;
