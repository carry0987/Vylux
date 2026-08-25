-- +goose Up

-- Jobs table — tracks all async media processing tasks.
CREATE TABLE IF NOT EXISTS jobs (
    id            TEXT        PRIMARY KEY,  -- asynq task ID
    type          TEXT        NOT NULL,     -- e.g. "image:thumbnail", "video:transcode"
    hash          TEXT        NOT NULL,     -- SHA-256 of the source file
    source        TEXT        NOT NULL,     -- source object key
    options       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    request_fingerprint TEXT  NOT NULL,     -- idempotency key derived from request payload
    status        TEXT        NOT NULL DEFAULT 'queued',
                                           -- queued | processing | completed | failed | canceled
    progress      INT         NOT NULL DEFAULT 0,  -- 0-100 percentage (video transcode)
    callback_url  TEXT        NOT NULL DEFAULT '',
    callback_status TEXT      NOT NULL DEFAULT 'pending',
                                           -- pending | sent | callback_failed
    retry_of_job_id TEXT      REFERENCES jobs(id) ON DELETE SET NULL,
                                           -- points to the failed job that spawned this retry
    error         TEXT,                     -- error message on failure
    results       JSONB,                    -- processing results (variants, keys, sizes)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for idempotency check: quickly find existing jobs by request fingerprint.
CREATE INDEX IF NOT EXISTS idx_jobs_request_fingerprint ON jobs (request_fingerprint);

-- Index for cleanup: find all jobs for a given hash.
CREATE INDEX IF NOT EXISTS idx_jobs_hash ON jobs (hash);

-- Index for retry lineage lookup.
CREATE INDEX IF NOT EXISTS idx_jobs_retry_of ON jobs (retry_of_job_id) WHERE retry_of_job_id IS NOT NULL;

-- Tracked synchronous image cache entries for targeted cleanup.
CREATE TABLE IF NOT EXISTS image_cache_entries (
    hash        TEXT        NOT NULL,
    cache_key   TEXT        NOT NULL,
    storage_key TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (hash, storage_key)
);

CREATE INDEX IF NOT EXISTS idx_image_cache_entries_hash ON image_cache_entries (hash);

-- Stream encryption keys — stores wrapped content keys for protected streaming assets.
CREATE TABLE IF NOT EXISTS stream_encryption_keys (
    id             UUID        PRIMARY KEY,
    source_hash    TEXT        NOT NULL,
    asset_type     TEXT        NOT NULL,
    packaging_type TEXT        NOT NULL,
    wrapped_key    BYTEA       NOT NULL,
    wrap_nonce     BYTEA       NOT NULL,
    kek_version    TEXT        NOT NULL DEFAULT 'v1',
    kid            TEXT        NOT NULL DEFAULT '',
    scheme         TEXT        NOT NULL DEFAULT 'cbcs',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_stream_encryption_keys_asset_type CHECK (asset_type IN ('audio', 'video')),
    CONSTRAINT chk_stream_encryption_keys_packaging_type CHECK (packaging_type IN ('hls')),
    CONSTRAINT uq_stream_encryption_keys_asset UNIQUE (source_hash, asset_type, packaging_type)
);

CREATE INDEX IF NOT EXISTS idx_stream_encryption_keys_source_hash ON stream_encryption_keys (source_hash);

-- Trigger to auto-update updated_at on jobs.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_jobs_updated_at
    BEFORE UPDATE ON jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();

-- +goose Down

DROP TRIGGER IF EXISTS trg_jobs_updated_at ON jobs;
DROP FUNCTION IF EXISTS update_updated_at;
DROP TABLE IF EXISTS image_cache_entries;
DROP TABLE IF EXISTS stream_encryption_keys;
DROP TABLE IF EXISTS jobs;
