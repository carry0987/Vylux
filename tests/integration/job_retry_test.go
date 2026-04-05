package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/internal/handler"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestJobRetry_FailedVideoFullCreatesStageJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, store, cleanup := newTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()
	if err := store.Put(ctx, cfg.SourceBucket, "uploads/retry.mp4", bytes.NewReader([]byte("retry-video")), "video/mp4"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	createBody := handler.JobRequest{
		Type:        queue.TypeVideoFull,
		Hash:        "retry-full-hash",
		Source:      "uploads/retry.mp4",
		CallbackURL: "http://example.com/callback",
		Options: map[string]any{
			"cover": map[string]any{
				"timestamp_sec": 2,
			},
			"preview": map[string]any{
				"start_sec": 3,
				"duration":  4,
				"width":     480,
				"fps":       10,
				"format":    "gif",
			},
			"transcode": map[string]any{
				"encrypt": true,
			},
		},
	}
	bodyJSON, _ := json.Marshal(createBody)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	defer resp.Body.Close()

	var created handler.JobResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.JobID == nil {
		t.Fatal("expected created job id")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer pool.Close()
	queries := dbq.New(pool)

	workflow := jobflow.VideoFullResult{
		Stages: jobflow.VideoFullStages{
			Source:    jobflow.StageState{Status: jobflow.StatusReady},
			Cover:     jobflow.StageState{Status: jobflow.StatusFailed, ErrorCode: "extract_failed", Error: "extract cover: boom", Retryable: true},
			Preview:   jobflow.StageState{Status: jobflow.StatusFailed, ErrorCode: "generate_failed", Error: "generate preview: boom", Retryable: true},
			Transcode: jobflow.StageState{Status: jobflow.StatusSkipped, Reason: "blocked_by_failed_dependencies"},
		},
		RetryPlan: jobflow.RetryPlan{
			Allowed:  true,
			Strategy: jobflow.RetryStrategyRetryTasks,
			JobTypes: []string{queue.TypeVideoCover, queue.TypeVideoPreview, queue.TypeVideoTranscode},
			Stages:   []string{jobflow.StageCover, jobflow.StagePreview, jobflow.StageTranscode},
			Reason:   "cover/preview stage failed",
		},
	}
	workflowJSON, _ := json.Marshal(workflow)
	if err := queries.UpdateJobFailure(ctx, dbq.UpdateJobFailureParams{
		ID:      *created.JobID,
		Error:   pgtype.Text{String: "video:full failed", Valid: true},
		Results: workflowJSON,
	}); err != nil {
		t.Fatalf("update job failure: %v", err)
	}

	retryReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs/"+*created.JobID+"/retry", nil)
	retryReq.Header.Set("X-API-Key", cfg.APIKey)

	retryResp, err := http.DefaultClient.Do(retryReq)
	if err != nil {
		t.Fatalf("retry job: %v", err)
	}
	defer retryResp.Body.Close()

	if retryResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", retryResp.StatusCode)
	}

	var retryBody handler.RetryJobResponse
	if err := json.NewDecoder(retryResp.Body).Decode(&retryBody); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}

	if retryBody.Strategy != jobflow.RetryStrategyRetryTasks {
		t.Fatalf("expected strategy %q, got %q", jobflow.RetryStrategyRetryTasks, retryBody.Strategy)
	}
	if len(retryBody.Jobs) != 3 {
		t.Fatalf("expected 3 retry jobs, got %d", len(retryBody.Jobs))
	}

	expectedTypes := map[string]bool{
		queue.TypeVideoCover:     true,
		queue.TypeVideoPreview:   true,
		queue.TypeVideoTranscode: true,
	}
	for _, retried := range retryBody.Jobs {
		if !expectedTypes[retried.Type] {
			t.Fatalf("unexpected retry job type %q", retried.Type)
		}
		job, err := queries.GetJob(ctx, retried.JobID)
		if err != nil {
			t.Fatalf("load retried job: %v", err)
		}
		if !job.RetryOfJobID.Valid || job.RetryOfJobID.String != *created.JobID {
			t.Fatalf("expected retry_of_job_id %q, got %#v", *created.JobID, job.RetryOfJobID)
		}
	}
}
