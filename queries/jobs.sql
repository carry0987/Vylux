-- name: CreateJob :exec
INSERT INTO jobs (id, type, hash, source, options, request_fingerprint, status, callback_url, retry_of_job_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;

-- name: GetActiveJobByFingerprint :one
SELECT * FROM jobs
WHERE request_fingerprint = $1 AND status NOT IN ('failed', 'cancelled')
ORDER BY created_at DESC
LIMIT 1;

-- name: UpdateJobStatus :exec
UPDATE jobs SET status = $2, error = $3 WHERE id = $1;

-- name: UpdateJobFailure :exec
UPDATE jobs SET status = 'failed', error = $2, results = $3 WHERE id = $1;

-- name: UpdateJobCompletion :exec
UPDATE jobs SET status = 'completed', progress = 100, error = NULL, results = $2 WHERE id = $1;

-- name: UpdateJobProgress :exec
UPDATE jobs SET progress = $2 WHERE id = $1;

-- name: UpdateJobResults :exec
UPDATE jobs SET results = $2 WHERE id = $1;

-- name: UpdateCallbackStatus :exec
UPDATE jobs SET callback_status = $2 WHERE id = $1;

-- name: ListJobsByHash :many
SELECT * FROM jobs WHERE hash = $1 ORDER BY created_at;

-- name: ListRetryJobs :many
SELECT * FROM jobs WHERE retry_of_job_id = $1 ORDER BY created_at;

-- name: DeleteJobsByHash :exec
DELETE FROM jobs WHERE hash = $1;
