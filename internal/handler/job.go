package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"Vylux/internal/db/dbq"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
)

// ── Request / Response DTOs ──

// JobRequest is the incoming JSON body for POST /api/jobs.
type JobRequest struct {
	Type        string         `json:"type"`
	Hash        string         `json:"hash"`
	Source      string         `json:"source"`
	Options     map[string]any `json:"options,omitempty"`
	CallbackURL string         `json:"callback_url"`
}

// JobResponse is the JSON response after creating or returning a job.
type JobResponse struct {
	JobID  *string `json:"job_id"` // nil when returning cached result
	Hash   string  `json:"hash"`
	Status string  `json:"status"`
	// Results is included only when the job is already completed.
	Results any `json:"results,omitempty"`
}

// JobStatusResponse is the JSON response for GET /api/jobs/:id.
type JobStatusResponse struct {
	JobID          string `json:"job_id"`
	Type           string `json:"type"`
	Hash           string `json:"hash"`
	Status         string `json:"status"`
	CallbackStatus string `json:"callback_status"`
	Progress       int32  `json:"progress"`
	RetryOfJobID   string `json:"retry_of_job_id,omitempty"`
	Error          string `json:"error,omitempty"`
	Results        any    `json:"results,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type RetryJobResponse struct {
	SourceJobID string         `json:"source_job_id"`
	Strategy    string         `json:"strategy"`
	Jobs        []RetryJobInfo `json:"jobs"`
}

type RetryJobInfo struct {
	JobID        string `json:"job_id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	RetryOfJobID string `json:"retry_of_job_id,omitempty"`
}

// ── Handler ──

// JobHandler handles asynchronous job creation and status queries.
type JobHandler struct {
	queries        *dbq.Queries
	queueClient    *queue.Client
	sourceStore    storage.Storage
	sourceBucket   string
	largeThreshold int64
	maxFileSize    int64
}

type jobRequestError struct {
	status  int
	message string
	err     error
}

func (e *jobRequestError) Error() string {
	return e.message
}

func (e *jobRequestError) Unwrap() error {
	return e.err
}

// NewJobHandler creates a JobHandler.
func NewJobHandler(
	queries *dbq.Queries,
	queueClient *queue.Client,
	sourceStore storage.Storage,
	sourceBucket string,
	largeThreshold int64,
	maxFileSize int64,
) *JobHandler {
	return &JobHandler{
		queries:        queries,
		queueClient:    queueClient,
		sourceStore:    sourceStore,
		sourceBucket:   sourceBucket,
		largeThreshold: largeThreshold,
		maxFileSize:    maxFileSize,
	}
}

// Create handles POST /api/jobs.
//
// Flow:
//  1. Validate request
//  2. Idempotency check — if a non-failed job exists for the same request fingerprint, return it
//  3. Enqueue task via asynq
//  4. Persist job row in DB
//  5. Return 202 Accepted
func (h *JobHandler) Create(c *echo.Context) error {
	req, err := decodeJobRequest(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON body")
	}

	if err := validateJobRequest(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := canonicalizeJobRequest(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ctx := c.Request().Context()
	fingerprint, err := requestFingerprint(req)
	if err != nil {
		slog.Error("build request fingerprint failed", apptracing.LogFields(ctx, "error", err)...)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to normalize request")
	}

	// ── Idempotency check ──
	existing, err := h.queries.GetActiveJobByFingerprint(ctx, fingerprint)
	if err == nil {
		// A non-failed/cancelled job already exists.
		resp := JobResponse{
			Hash:   existing.Hash,
			Status: existing.Status,
		}
		if existing.Status == "completed" {
			resp.Results = existing.Results
			return c.JSON(http.StatusOK, resp)
		}
		id := existing.ID
		resp.JobID = &id
		return c.JSON(http.StatusOK, resp)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("idempotency check failed", apptracing.LogFields(ctx, "error", err)...)
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	created, err := h.createOrReuseJob(ctx, req, fingerprint, "")
	if err != nil {
		return err
	}
	jobID := created.JobID
	return c.JSON(http.StatusAccepted, JobResponse{
		JobID:  &jobID,
		Hash:   req.Hash,
		Status: created.Status,
	})
}

// GetStatus handles GET /api/jobs/:id.
func (h *JobHandler) GetStatus(c *echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing job id")
	}

	job, err := h.queries.GetJob(c.Request().Context(), jobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "job not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	resp := JobStatusResponse{
		JobID:          job.ID,
		Type:           job.Type,
		Hash:           job.Hash,
		Status:         job.Status,
		CallbackStatus: job.CallbackStatus,
		Progress:       job.Progress,
		CreatedAt:      job.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      job.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	if job.RetryOfJobID.Valid {
		resp.RetryOfJobID = job.RetryOfJobID.String
	}
	if job.Error.Valid {
		resp.Error = job.Error.String
	}
	if job.Results != nil {
		resp.Results = job.Results
	}

	return c.JSON(http.StatusOK, resp)
}

// Retry handles POST /api/jobs/:id/retry.
func (h *JobHandler) Retry(c *echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing job id")
	}

	ctx := c.Request().Context()
	job, err := h.queries.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "job not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	if job.Status != "failed" {
		return echo.NewHTTPError(http.StatusConflict, "only failed jobs can be retried")
	}

	retryReqs, strategy, err := h.buildRetryRequests(job)
	if err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}

	resp := RetryJobResponse{
		SourceJobID: job.ID,
		Strategy:    strategy,
		Jobs:        make([]RetryJobInfo, 0, len(retryReqs)),
	}

	for _, req := range retryReqs {
		if err := canonicalizeJobRequest(&req); err != nil {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		fingerprint, err := requestFingerprint(req)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to normalize retry request")
		}

		created, err := h.createOrReuseJob(ctx, req, fingerprint, job.ID)
		if err != nil {
			return err
		}

		resp.Jobs = append(resp.Jobs, RetryJobInfo{
			JobID:        created.JobID,
			Type:         req.Type,
			Status:       created.Status,
			RetryOfJobID: job.ID,
		})
	}

	return c.JSON(http.StatusAccepted, resp)
}

// ── Private helpers ──

// validJobTypes lists accepted values for the "type" field.
var validJobTypes = map[string]bool{
	queue.TypeImageThumbnail: true,
	queue.TypeVideoCover:     true,
	queue.TypeVideoPreview:   true,
	queue.TypeVideoTranscode: true,
	queue.TypeVideoFull:      true,
}

func validateJobRequest(r *JobRequest) error {
	if !validJobTypes[r.Type] {
		return fmt.Errorf("unsupported job type: %q", r.Type)
	}
	if r.Hash == "" {
		return fmt.Errorf("hash is required")
	}
	if r.Source == "" {
		return fmt.Errorf("source is required")
	}
	if err := validateCallbackURL(r.CallbackURL); err != nil {
		return err
	}
	if err := validateJobOptions(r.Type, r.Options); err != nil {
		return err
	}
	return nil
}

func validateCallbackURL(raw string) error {
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("callback_url must be a valid URL: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("callback_url must use http:// or https://")
	}

	if u.Host == "" {
		return fmt.Errorf("callback_url must include a host")
	}

	return nil
}

func validateJobOptions(jobType string, opts map[string]any) error {
	switch jobType {
	case queue.TypeVideoCover:
		_, err := parseVideoCoverOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeVideoPreview:
		_, err := parseVideoPreviewOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeVideoTranscode:
		_, err := parseVideoTranscodeOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	case queue.TypeVideoFull:
		_, err := parseVideoFullOptions(opts)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
	}
	return nil
}

func decodeJobRequest(c *echo.Context) (JobRequest, error) {
	var req JobRequest
	dec := json.NewDecoder(c.Request().Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return JobRequest{}, err
	}

	return req, nil
}

func canonicalizeJobRequest(r *JobRequest) error {
	if r.Options == nil {
		r.Options = map[string]any{}
	}

	switch r.Type {
	case queue.TypeVideoCover:
		parsed, err := parseVideoCoverOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoPreview:
		parsed, err := parseVideoPreviewOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoTranscode:
		parsed, err := parseVideoTranscodeOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	case queue.TypeVideoFull:
		parsed, err := parseVideoFullOptions(r.Options)
		if err != nil {
			return fmt.Errorf("invalid options: %w", err)
		}
		canonicalizeVideoFullOptions(&parsed)
		canonical, err := structToOptionsMap(parsed)
		if err != nil {
			return fmt.Errorf("canonicalize options: %w", err)
		}
		r.Options = canonical
	}

	return nil
}

func parseVideoCoverOptions(opts map[string]any) (queue.VideoCoverOptions, error) {
	return decodeOptionsStrict[queue.VideoCoverOptions](opts)
}

func parseVideoPreviewOptions(opts map[string]any) (queue.VideoPreviewOptions, error) {
	return decodeOptionsStrict[queue.VideoPreviewOptions](opts)
}

func parseVideoTranscodeOptions(opts map[string]any) (queue.VideoTranscodeOptions, error) {
	return decodeOptionsStrict[queue.VideoTranscodeOptions](opts)
}

func parseVideoFullOptions(opts map[string]any) (queue.VideoFullOptions, error) {
	return decodeOptionsStrict[queue.VideoFullOptions](opts)
}

func decodeOptionsStrict[T any](opts map[string]any) (T, error) {
	var decoded T
	if opts == nil {
		return decoded, nil
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return decoded, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

func structToOptionsMap[T any](opts T) (map[string]any, error) {
	data, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func canonicalizeVideoFullOptions(opts *queue.VideoFullOptions) {
	if opts == nil {
		return
	}
	if opts.Cover != nil && *opts.Cover == (queue.VideoCoverOptions{}) {
		opts.Cover = nil
	}
	if opts.Preview != nil && *opts.Preview == (queue.VideoPreviewOptions{}) {
		opts.Preview = nil
	}
	if opts.Transcode != nil && *opts.Transcode == (queue.VideoTranscodeOptions{}) {
		opts.Transcode = nil
	}
}

func requestFingerprint(r JobRequest) (string, error) {
	payload, err := json.Marshal(struct {
		Type    string         `json:"type"`
		Hash    string         `json:"hash"`
		Source  string         `json:"source"`
		Options map[string]any `json:"options"`
	}{
		Type:    r.Type,
		Hash:    r.Hash,
		Source:  r.Source,
		Options: r.Options,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func optionsJSON(opts map[string]any) (json.RawMessage, error) {
	if opts == nil {
		return json.RawMessage(`{}`), nil
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func parseOptions(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var opts map[string]any
	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = map[string]any{}
	}
	return opts, nil
}

func (h *JobHandler) createOrReuseJob(ctx context.Context, req JobRequest, fingerprint string, retryOfJobID string) (*RetryJobInfo, error) {
	existing, err := h.queries.GetActiveJobByFingerprint(ctx, fingerprint)
	if err == nil {
		return &RetryJobInfo{JobID: existing.ID, Type: existing.Type, Status: existing.Status}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("idempotency check failed", apptracing.LogFields(ctx, "error", err)...)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	taskInfo, err := h.enqueueTask(ctx, req)
	if err != nil {
		if requestErr, ok := errors.AsType[*jobRequestError](err); ok {
			return nil, echo.NewHTTPError(requestErr.status, requestErr.message)
		}
		slog.Error("enqueue failed", apptracing.LogFields(ctx,
			"type", req.Type,
			"hash", req.Hash,
			"error", err,
		)...)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "failed to enqueue task")
	}

	options, err := optionsJSON(req.Options)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "failed to serialize options")
	}

	retryOf := pgtype.Text{}
	if retryOfJobID != "" {
		retryOf = pgtype.Text{String: retryOfJobID, Valid: true}
	}

	if err := h.queries.CreateJob(ctx, dbq.CreateJobParams{
		ID:                 taskInfo.ID,
		Type:               req.Type,
		Hash:               req.Hash,
		Source:             req.Source,
		Options:            options,
		RequestFingerprint: fingerprint,
		Status:             "queued",
		CallbackUrl:        req.CallbackURL,
		RetryOfJobID:       retryOf,
	}); err != nil {
		slog.Error("create job row failed", apptracing.LogFields(ctx, "id", taskInfo.ID, "error", err)...)
	}

	return &RetryJobInfo{JobID: taskInfo.ID, Type: req.Type, Status: "queued", RetryOfJobID: retryOfJobID}, nil
}

func (h *JobHandler) buildRetryRequests(job dbq.Job) ([]JobRequest, string, error) {
	baseOptions, err := parseOptions(job.Options)
	if err != nil {
		return nil, "", fmt.Errorf("stored job options are invalid")
	}

	switch job.Type {
	case queue.TypeVideoFull:
		fullOptions, err := parseVideoFullOptions(baseOptions)
		if err != nil {
			return nil, "", fmt.Errorf("stored job options are invalid")
		}
		canonicalizeVideoFullOptions(&fullOptions)

		if len(job.Results) == 0 {
			return []JobRequest{jobRequestFromStored(job.Type, job, baseOptions)}, jobflow.RetryStrategyRetryJob, nil
		}

		var result jobflow.VideoFullResult
		if err := json.Unmarshal(job.Results, &result); err != nil {
			return nil, "", fmt.Errorf("stored workflow state is invalid")
		}
		if !result.RetryPlan.Allowed || len(result.RetryPlan.JobTypes) == 0 {
			return nil, "", fmt.Errorf("job has no retry plan")
		}

		requests := make([]JobRequest, 0, len(result.RetryPlan.JobTypes))
		for _, jobType := range result.RetryPlan.JobTypes {
			req, err := retryRequestForVideoFull(jobType, job, fullOptions)
			if err != nil {
				return nil, "", err
			}
			requests = append(requests, req)
		}
		return requests, result.RetryPlan.Strategy, nil
	default:
		return []JobRequest{jobRequestFromStored(job.Type, job, baseOptions)}, jobflow.RetryStrategyRetryJob, nil
	}
}

func jobRequestFromStored(jobType string, job dbq.Job, opts map[string]any) JobRequest {
	return JobRequest{
		Type:        jobType,
		Hash:        job.Hash,
		Source:      job.Source,
		Options:     cloneOptions(opts),
		CallbackURL: job.CallbackUrl,
	}
}

func retryRequestForVideoFull(jobType string, job dbq.Job, opts queue.VideoFullOptions) (JobRequest, error) {
	filtered := map[string]any{}
	switch jobType {
	case queue.TypeVideoCover:
		if opts.Cover != nil {
			var err error
			filtered, err = structToOptionsMap(*opts.Cover)
			if err != nil {
				return JobRequest{}, fmt.Errorf("build cover retry options: %w", err)
			}
		}
	case queue.TypeVideoPreview:
		if opts.Preview != nil {
			var err error
			filtered, err = structToOptionsMap(*opts.Preview)
			if err != nil {
				return JobRequest{}, fmt.Errorf("build preview retry options: %w", err)
			}
		}
	case queue.TypeVideoTranscode:
		if opts.Transcode != nil {
			var err error
			filtered, err = structToOptionsMap(*opts.Transcode)
			if err != nil {
				return JobRequest{}, fmt.Errorf("build transcode retry options: %w", err)
			}
		}
	default:
		var err error
		filtered, err = structToOptionsMap(opts)
		if err != nil {
			return JobRequest{}, fmt.Errorf("build retry options: %w", err)
		}
	}

	return JobRequest{
		Type:        jobType,
		Hash:        job.Hash,
		Source:      job.Source,
		Options:     filtered,
		CallbackURL: job.CallbackUrl,
	}, nil
}

func cloneOptions(opts map[string]any) map[string]any {
	if len(opts) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(opts))
	for k, v := range opts {
		cloned[k] = v
	}
	return cloned
}

func (h *JobHandler) sourceSizeForRequest(ctx context.Context, req JobRequest) (int64, error) {
	switch req.Type {
	case queue.TypeVideoTranscode, queue.TypeVideoFull:
	default:
		return 0, nil
	}

	size, err := h.sourceStore.Size(ctx, h.sourceBucket, req.Source)
	if err != nil {
		if storage.IsNotFound(err) {
			return 0, &jobRequestError{
				status:  http.StatusBadRequest,
				message: "source object not found",
				err:     err,
			}
		}

		return 0, fmt.Errorf("lookup source size: %w", err)
	}

	if h.maxFileSize > 0 && size > h.maxFileSize {
		return 0, &jobRequestError{
			status:  http.StatusRequestEntityTooLarge,
			message: fmt.Sprintf("source file exceeds MAX_FILE_SIZE (%d bytes)", h.maxFileSize),
		}
	}

	return size, nil
}

// enqueueTask dispatches the request to the appropriate queue method.
func (h *JobHandler) enqueueTask(ctx context.Context, req JobRequest) (*taskInfoCompat, error) {
	traceCarrier := apptracing.CaptureCarrier(ctx)
	sourceSize, err := h.sourceSizeForRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	switch req.Type {
	case queue.TypeImageThumbnail:
		payload := queue.ImageThumbnailPayload{
			TraceCarrier: traceCarrier,
			Hash:         req.Hash,
			Source:       req.Source,
			Outputs:      parseThumbnailOutputs(req.Options),
			CallbackURL:  req.CallbackURL,
		}
		info, err := h.queueClient.EnqueueImageThumbnail(ctx, payload)
		if err != nil {
			return nil, err
		}
		return &taskInfoCompat{ID: info.ID, Queue: info.Queue}, nil

	case queue.TypeVideoCover:
		options, err := parseVideoCoverOptions(req.Options)
		if err != nil {
			return nil, err
		}
		payload := queue.VideoCoverPayload{
			TraceCarrier: traceCarrier,
			Hash:         req.Hash,
			Source:       req.Source,
			TimestampSec: options.TimestampSec,
			CallbackURL:  req.CallbackURL,
		}
		info, err := h.queueClient.EnqueueVideoCover(ctx, payload)
		if err != nil {
			return nil, err
		}
		return &taskInfoCompat{ID: info.ID, Queue: info.Queue}, nil

	case queue.TypeVideoPreview:
		options, err := parseVideoPreviewOptions(req.Options)
		if err != nil {
			return nil, err
		}
		payload := queue.VideoPreviewPayload{
			TraceCarrier: traceCarrier,
			Hash:         req.Hash,
			Source:       req.Source,
			StartSec:     options.StartSec,
			Duration:     options.Duration,
			Width:        options.Width,
			FPS:          options.FPS,
			Format:       options.Format,
			CallbackURL:  req.CallbackURL,
		}
		info, err := h.queueClient.EnqueueVideoPreview(ctx, payload)
		if err != nil {
			return nil, err
		}
		return &taskInfoCompat{ID: info.ID, Queue: info.Queue}, nil

	case queue.TypeVideoTranscode:
		options, err := parseVideoTranscodeOptions(req.Options)
		if err != nil {
			return nil, err
		}
		payload := queue.VideoTranscodePayload{
			TraceCarrier: traceCarrier,
			Hash:         req.Hash,
			Source:       req.Source,
			Encrypt:      options.Encrypt,
			CallbackURL:  req.CallbackURL,
		}
		info, err := h.queueClient.EnqueueVideoTranscode(ctx, payload, sourceSize, h.largeThreshold)
		if err != nil {
			return nil, err
		}
		return &taskInfoCompat{ID: info.ID, Queue: info.Queue}, nil

	case queue.TypeVideoFull:
		options, err := parseVideoFullOptions(req.Options)
		if err != nil {
			return nil, err
		}
		canonicalizeVideoFullOptions(&options)
		payload := queue.VideoFullPayload{
			TraceCarrier: traceCarrier,
			Hash:         req.Hash,
			Source:       req.Source,
			Options:      options,
			CallbackURL:  req.CallbackURL,
		}
		info, err := h.queueClient.EnqueueVideoFull(ctx, payload, sourceSize, h.largeThreshold)
		if err != nil {
			return nil, err
		}
		return &taskInfoCompat{ID: info.ID, Queue: info.Queue}, nil

	default:
		return nil, fmt.Errorf("unsupported type: %s", req.Type)
	}
}

// taskInfoCompat is a minimal subset of asynq.TaskInfo for internal use.
type taskInfoCompat struct {
	ID    string
	Queue string
}

// parseThumbnailOutputs converts the "outputs" key from options into typed structs.
func parseThumbnailOutputs(opts map[string]any) []queue.ThumbnailOutput {
	raw, ok := opts["outputs"]
	if !ok {
		return nil
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	out := make([]queue.ThumbnailOutput, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		o := queue.ThumbnailOutput{}
		if v, ok := m["variant"].(string); ok {
			o.Variant = v
		}
		if v, ok := m["width"].(float64); ok {
			o.Width = int(v)
		}
		if v, ok := m["height"].(float64); ok {
			o.Height = int(v)
		}
		if v, ok := m["format"].(string); ok {
			o.Format = v
		}
		out = append(out, o)
	}

	return out
}
