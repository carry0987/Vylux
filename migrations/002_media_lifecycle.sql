-- +goose Up

-- Permanent, exact source fences created by the host durable-GC path.
-- Legacy administrator purge deliberately does not write this table.
CREATE TABLE IF NOT EXISTS media_tombstones (
    hash       TEXT        NOT NULL,
    source     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (hash, source)
);

CREATE INDEX IF NOT EXISTS idx_media_tombstones_hash ON media_tombstones (hash);

-- A restart-safe, bounded audit of the pre-namespace synchronous image cache.
-- Strict cleanup remains fail-closed until every historical cache key has been
-- inspected. The cursor only advances after a complete successful page.
CREATE TABLE IF NOT EXISTS media_lifecycle_readiness (
    singleton            BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    cache_audit_cursor   TEXT        NOT NULL DEFAULT '',
    cache_audit_complete BOOLEAN     NOT NULL DEFAULT FALSE,
    cache_audit_armed    BOOLEAN     NOT NULL DEFAULT FALSE,
    cache_audit_error    TEXT        NOT NULL DEFAULT '',
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO media_lifecycle_readiness (singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS media_lifecycle_readiness;
DROP TABLE IF EXISTS media_tombstones;
