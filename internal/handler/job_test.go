package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"Vylux/internal/db/dbq"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"
	"Vylux/tests/testutil"

	"github.com/labstack/echo/v5"
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

	retryReqs, strategy, err := h.buildRetryRequests(&dbq.Job{
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

	retryReqs, strategy, err := h.buildRetryRequests(&dbq.Job{
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

func TestValidateJobRequest_AudioTranscodeAcceptsStructuredOptions(t *testing.T) {
	req := JobRequest{
		Type:   queue.TypeAudioTranscode,
		Hash:   "hash123",
		Source: "uploads/audio.flac",
		Options: map[string]any{
			"hls":         true,
			"mp3":         true,
			"mp3_bitrate": "192k",
		},
	}

	if err := validateJobRequest(&req); err != nil {
		t.Fatalf("expected audio transcode request to be valid, got %v", err)
	}
	if err := canonicalizeJobRequest(&req); err != nil {
		t.Fatalf("canonicalizeJobRequest: %v", err)
	}
	if req.Options["mp3_bitrate"] != "192k" {
		t.Fatalf("expected bitrate to be preserved, got %#v", req.Options["mp3_bitrate"])
	}
}

func TestCanonicalizeJobRequest_AudioTranscodeDefaultsOutputs(t *testing.T) {
	req := JobRequest{
		Type:    queue.TypeAudioTranscode,
		Hash:    "hash123",
		Source:  "uploads/audio.flac",
		Options: map[string]any{},
	}

	if err := canonicalizeJobRequest(&req); err != nil {
		t.Fatalf("canonicalizeJobRequest: %v", err)
	}
	if req.Options["hls"] != true || req.Options["mp3"] != true || req.Options["flac"] != true {
		t.Fatalf("expected default outputs to be enabled, got %#v", req.Options)
	}
	if req.Options["mp3_bitrate"] != "320k" {
		t.Fatalf("expected default mp3 bitrate 320k, got %#v", req.Options["mp3_bitrate"])
	}
}

func TestDecodeAudioJobRequest(t *testing.T) {
	body := `{
		"source":{"hash":"hash123","key":"uploads/audio.flac"},
		"pipeline":{
			"package":{"hls":{"enabled":true,"profile":"stream_aac_standard"}},
			"downloads":[{"profile":"download_mp3_high"}]
		},
		"delivery":{"callback_url":"https://example.com/callback"}
	}`

	req, err := decodeAudioJobRequestContext(t, body)
	if err != nil {
		t.Fatalf("decodeAudioJobRequest: %v", err)
	}
	if req.Type != queue.TypeAudioTranscode {
		t.Fatalf("type = %q, want %q", req.Type, queue.TypeAudioTranscode)
	}
	if req.Hash != "hash123" || req.Source != "uploads/audio.flac" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestDecodeVideoJobRequest(t *testing.T) {
	body := `{
		"source":{"hash":"hash123","key":"uploads/video.mp4"},
		"pipeline":{"package":{"hls":{"enabled":true,"profile":"stream_video_standard","encryption":{"enabled":true}}}},
		"delivery":{"callback_url":"https://example.com/callback"}
	}`

	req, err := decodeVideoJobRequestContext(t, body)
	if err != nil {
		t.Fatalf("decodeVideoJobRequest: %v", err)
	}
	if req.Type != queue.TypeVideoTranscode {
		t.Fatalf("type = %q, want %q", req.Type, queue.TypeVideoTranscode)
	}
	if req.Hash != "hash123" || req.Source != "uploads/video.mp4" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestRequestFingerprint_StructuredMatchesLegacyAudio(t *testing.T) {
	structuredBody := `{
		"source":{"hash":"hash123","key":"uploads/audio.flac"},
		"pipeline":{
			"package":{"hls":{"enabled":true,"profile":"stream_aac_standard"}},
			"downloads":[{"format":"mp3","bitrate":"192k"},{"format":"flac"}]
		}
	}`

	structuredReq, err := decodeAudioJobRequestContext(t, structuredBody)
	if err != nil {
		t.Fatalf("decode structured request: %v", err)
	}
	if err := canonicalizeJobRequest(&structuredReq); err != nil {
		t.Fatalf("canonicalize structured request: %v", err)
	}

	legacyReq := JobRequest{
		Type:   queue.TypeAudioTranscode,
		Hash:   "hash123",
		Source: "uploads/audio.flac",
		Options: map[string]any{
			"hls":         true,
			"mp3":         true,
			"flac":        true,
			"mp3_bitrate": "192k",
		},
	}
	if err := canonicalizeJobRequest(&legacyReq); err != nil {
		t.Fatalf("canonicalize legacy request: %v", err)
	}

	structuredFingerprint, err := requestFingerprint(structuredReq)
	if err != nil {
		t.Fatalf("requestFingerprint(structured): %v", err)
	}
	legacyFingerprint, err := requestFingerprint(legacyReq)
	if err != nil {
		t.Fatalf("requestFingerprint(legacy): %v", err)
	}
	if structuredFingerprint != legacyFingerprint {
		t.Fatalf("fingerprints differ: structured=%q legacy=%q", structuredFingerprint, legacyFingerprint)
	}
}

func TestEnqueueTask_RejectsMissingAudioSource(t *testing.T) {
	h := &JobHandler{
		sourceStore:    testutil.NewFakeStore(),
		sourceBucket:   "source",
		largeThreshold: 5,
		maxFileSize:    10,
	}

	_, err := h.enqueueTask(t.Context(), JobRequest{
		Type:   queue.TypeAudioTranscode,
		Hash:   "hash123",
		Source: "uploads/missing.flac",
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

func TestValidateJobRequest_CallbackURL(t *testing.T) {
	tests := []struct {
		name        string
		callbackURL string
		wantErrPart string
	}{
		{name: "empty allowed", callbackURL: ""},
		{name: "http allowed", callbackURL: "http://example.com/callback"},
		{name: "https allowed", callbackURL: "https://example.com/callback"},
		{name: "reject invalid scheme", callbackURL: "ftp://example.com/callback", wantErrPart: "callback_url must use http:// or https://"},
		{name: "reject missing host", callbackURL: "https:///callback", wantErrPart: "callback_url must include a host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := JobRequest{
				Type:        queue.TypeVideoPreview,
				Hash:        "hash123",
				Source:      "uploads/video.mp4",
				CallbackURL: tt.callbackURL,
			}

			err := validateJobRequest(&req)
			if tt.wantErrPart == "" {
				if err != nil {
					t.Fatalf("expected callback_url %q to be accepted, got %v", tt.callbackURL, err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected callback_url %q to be rejected", tt.callbackURL)
			}
			if !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrPart, err)
			}
		})
	}
}

func decodeAudioJobRequestContext(t *testing.T, body string) (JobRequest, error) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/audio/jobs", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	resp := httptest.NewRecorder()
	c := e.NewContext(req, resp)
	return decodeAudioJobRequest(c)
}

func decodeVideoJobRequestContext(t *testing.T, body string) (JobRequest, error) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/video/jobs", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	resp := httptest.NewRecorder()
	c := e.NewContext(req, resp)
	return decodeVideoJobRequest(c)
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
