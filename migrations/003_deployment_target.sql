-- +goose Up

-- One logical Vylux deployment is permanently bound to the exact source and
-- media backends it owns. All four columns are nullable only for the v1 -> v2
-- rollout window; startup binds them atomically before serving or working.
ALTER TABLE media_lifecycle_readiness
    ADD COLUMN protocol_version SMALLINT,
    ADD COLUMN deployment_id TEXT,
    ADD COLUMN source_backend_identity TEXT,
    ADD COLUMN media_backend_identity TEXT,
    ADD CONSTRAINT media_deployment_target_all_or_none CHECK (
        (
            protocol_version IS NULL
            AND deployment_id IS NULL
            AND source_backend_identity IS NULL
            AND media_backend_identity IS NULL
        )
        OR
        (
            protocol_version IS NOT NULL
            AND deployment_id IS NOT NULL
            AND source_backend_identity IS NOT NULL
            AND media_backend_identity IS NOT NULL
        )
    ),
    ADD CONSTRAINT media_deployment_protocol_v2 CHECK (
        protocol_version IS NULL OR protocol_version = 2
    ),
    ADD CONSTRAINT media_deployment_id_uuid CHECK (
        deployment_id IS NULL
        OR deployment_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    ),
    ADD CONSTRAINT media_source_backend_identity_v1 CHECK (
        source_backend_identity IS NULL
        OR source_backend_identity ~ '^sha256:v1:[0-9a-f]{64}$'
    ),
    ADD CONSTRAINT media_media_backend_identity_v1 CHECK (
        media_backend_identity IS NULL
        OR media_backend_identity ~ '^sha256:v1:[0-9a-f]{64}$'
    );

-- +goose Down

ALTER TABLE media_lifecycle_readiness
    DROP COLUMN media_backend_identity,
    DROP COLUMN source_backend_identity,
    DROP COLUMN deployment_id,
    DROP COLUMN protocol_version;
