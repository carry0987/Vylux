package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"Vylux/internal/db/dbq"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"
	"Vylux/tests/testutil"
)

func TestBuildRetryRequests_FailedVideoFullBuildsStageRetries(t *testing.T) {
	h := &JobHandler{}

	workflow := jobflow.VideoFullResult{
		Stages: jobflow.VideoFullStages{
			Source:    jobflow.StageState{Status: jobflow.StatusReady},
			Cover:     jobflow.StageState{Status: jobflow.StatusFailed, ErrorCode: "extract_failed"},
			Preview:   jobflow.StageState{Status: jobflow.StatusFailed, ErrorCode: "generate_failed"},
			Transcode: jobflow.StageState{Status: jobflow.StatusSkipped, Reason: "blocked_by_failed_dependencies"},
		},
		RetryPlan: jobflow.RetryPlan{
			Allowed:  true,
			Strategy: jobflow.RetryStrategyRetryTasks,
			JobTypes: []string{queue.TypeVideoCover, queue.TypeVideoPreview, queue.TypeVideoTranscode},
			Stages:   []string{jobflow.StageCover, jobflow.StagePreview, jobflow.StageTranscode},
		},
	}
	workflowJSON, err := json.Marshal(workflow)
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	optionsJSON := json.RawMessage(`{"cover":{"timestamp_sec":2},"preview":{"start_sec":3,"duration":4,"width":480,"fps":12,"format":"gif"},"transcode":{"encrypt":true}}`)

	retryReqs, strategy, err := h.buildRetryRequests(dbq.Job{
		Type:        queue.TypeVideoFull,
		Hash:        "hash123",
		Source:      "uploads/video.mp4",
		Options:     optionsJSON,
		CallbackUrl: "http://example.com/callback",
		Results:     workflowJSON,
	})
	if err != nil {
		t.Fatalf("buildRetryRequests: %v", err)
	}

	if strategy != jobflow.RetryStrategyRetryTasks {
		t.Fatalf("expected strategy %q, got %q", jobflow.RetryStrategyRetryTasks, strategy)
	}
	if len(retryReqs) != 3 {
		t.Fatalf("expected 3 retry requests, got %d", len(retryReqs))
	}

	if retryReqs[0].Type != queue.TypeVideoCover {
		t.Fatalf("expected first retry to be %q, got %q", queue.TypeVideoCover, retryReqs[0].Type)
	}
	if retryReqs[0].Options["timestamp_sec"].(float64) != 2 {
		t.Fatalf("expected cover timestamp 2, got %#v", retryReqs[0].Options["timestamp_sec"])
	}

	if retryReqs[1].Type != queue.TypeVideoPreview {
		t.Fatalf("expected second retry to be %q, got %q", queue.TypeVideoPreview, retryReqs[1].Type)
	}
	if retryReqs[1].Options["format"].(string) != "gif" {
		t.Fatalf("expected preview format gif, got %#v", retryReqs[1].Options["format"])
	}

	if retryReqs[2].Type != queue.TypeVideoTranscode {
		t.Fatalf("expected third retry to be %q, got %q", queue.TypeVideoTranscode, retryReqs[2].Type)
	}
	if retryReqs[2].Options["encrypt"].(bool) != true {
		t.Fatalf("expected transcode retry encrypt=true, got %#v", retryReqs[2].Options["encrypt"])
	}
	if len(retryReqs[2].Options) != 1 {
		t.Fatalf("expected transcode retry to only carry encrypt, got %#v", retryReqs[2].Options)
	}
}

func TestBuildRetryRequests_SingleStageRetryReusesStoredRequest(t *testing.T) {
	h := &JobHandler{}

	retryReqs, strategy, err := h.buildRetryRequests(dbq.Job{
		Type:        queue.TypeVideoPreview,
		Hash:        "hash123",
		Source:      "uploads/video.mp4",
		Options:     json.RawMessage(`{"start_sec":5,"duration":3,"width":320,"fps":8,"format":"gif"}`),
		CallbackUrl: "http://example.com/callback",
	})
	if err != nil {
		t.Fatalf("buildRetryRequests: %v", err)
	}
	if strategy != jobflow.RetryStrategyRetryJob {
		t.Fatalf("expected strategy %q, got %q", jobflow.RetryStrategyRetryJob, strategy)
	}
	if len(retryReqs) != 1 {
		t.Fatalf("expected 1 retry request, got %d", len(retryReqs))
	}
	if retryReqs[0].Type != queue.TypeVideoPreview {
		t.Fatalf("expected retry type %q, got %q", queue.TypeVideoPreview, retryReqs[0].Type)
	}
	if retryReqs[0].Options["format"].(string) != "gif" {
		t.Fatalf("expected preview format gif, got %#v", retryReqs[0].Options["format"])
	}
}

func TestValidateJobRequest_VideoFullRejectsFlatOptions(t *testing.T) {
	req := JobRequest{
		Type:   queue.TypeVideoFull,
		Hash:   "hash123",
		Source: "uploads/video.mp4",
		Options: map[string]any{
			"timestamp_sec": 1,
		},
	}

	err := validateJobRequest(&req)
	if err == nil {
		t.Fatal("expected flat video:full options to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("expected invalid options error, got %v", err)
	}
}

func TestEnqueueTask_RejectsOversizedVideoSource(t *testing.T) {
	store := testutil.NewFakeStore()
	sourcePath := filepath.Join("uploads", "video.mp4")
	if err := store.Put(t.Context(), "source", sourcePath, strings.NewReader("1234567890"), "video/mp4"); err != nil {
		t.Fatalf("put source: %v", err)
	}

	h := &JobHandler{
		sourceStore:    store,
		sourceBucket:   "source",
		largeThreshold: 5,
		maxFileSize:    4,
	}

	_, err := h.enqueueTask(t.Context(), JobRequest{
		Type:   queue.TypeVideoTranscode,
		Hash:   "hash123",
		Source: sourcePath,
	})
	if err == nil {
		t.Fatal("expected oversize error")
	}

	requestErr, ok := errors.AsType[*jobRequestError](err)
	if !ok {
		t.Fatalf("expected jobRequestError, got %T", err)
	}
	if requestErr.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", requestErr.status, http.StatusRequestEntityTooLarge)
	}
}

func TestEnqueueTask_RejectsMissingVideoSource(t *testing.T) {
	h := &JobHandler{
		sourceStore:    testutil.NewFakeStore(),
		sourceBucket:   "source",
		largeThreshold: 5,
		maxFileSize:    10,
	}

	_, err := h.enqueueTask(t.Context(), JobRequest{
		Type:   queue.TypeVideoFull,
		Hash:   "hash123",
		Source: "uploads/missing.mp4",
	})
	if err == nil {
		t.Fatal("expected missing source error")
	}

	requestErr, ok := errors.AsType[*jobRequestError](err)
	if !ok {
		t.Fatalf("expected jobRequestError, got %T", err)
	}
	if requestErr.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", requestErr.status, http.StatusBadRequest)
	}
}
