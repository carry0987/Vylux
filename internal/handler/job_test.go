package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		Hash:   strings.Repeat("a", 64),
		Source: "uploads/" + strings.Repeat("a", 64) + "-upload-id.mp4",
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
				Hash:        strings.Repeat("b", 64),
				Source:      "uploads/" + strings.Repeat("b", 64) + ".mp4",
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
func TestValidateJobRequestRequiresMatchingProductionLifecycleIdentity(t *testing.T) {
	hash := strings.Repeat("c", 64)
	tests := []struct {
		name    string
		hash    string
		source  string
		wantErr string
	}{
		{
			name:   "host hash extension path",
			hash:   strings.ToUpper(hash),
			source: "uploads/" + strings.ToUpper(hash) + ".png",
		},
		{
			name:   "host hash upload id path",
			hash:   hash,
			source: "tenant/media/" + hash + "-550e8400-e29b-41d4-a716-446655440000.jpeg",
		},
		{
			name:    "invalid hash",
			hash:    "hash123",
			source:  "uploads/hash123.png",
			wantErr: "64-character hexadecimal",
		},
		{
			name:    "unattributable source",
			hash:    hash,
			source:  "uploads/legacy-name.png",
			wantErr: "attributable content hash",
		},
		{
			name:    "mismatched source",
			hash:    hash,
			source:  "uploads/" + strings.Repeat("d", 64) + ".png",
			wantErr: "does not match hash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := JobRequest{
				Type:   queue.TypeImageThumbnail,
				Hash:   tt.hash,
				Source: tt.source,
				Options: map[string]any{
					"outputs": []any{map[string]any{"variant": "thumb", "width": 64, "format": "webp"}},
				},
			}
			err := validateJobRequest(&req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid identity, got %v", err)
				}
				if req.Hash != hash {
					t.Fatalf("expected normalized hash %q, got %q", hash, req.Hash)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCanonicalizeJobRequestPreservesWhitespaceDistinctSourceIdentity(t *testing.T) {
	hash := strings.Repeat("e", 64)
	rawSource := "uploads/ " + hash + ".png "
	raw := JobRequest{
		Type:   queue.TypeImageThumbnail,
		Hash:   hash,
		Source: rawSource,
	}
	canonical := raw
	canonical.Source = "uploads/" + hash + ".png"

	if err := canonicalizeJobRequest(&raw); err != nil {
		t.Fatalf("canonicalize raw source: %v", err)
	}
	if err := canonicalizeJobRequest(&canonical); err != nil {
		t.Fatalf("canonicalize whitespace-free source: %v", err)
	}
	if raw.Source != rawSource {
		t.Fatalf("canonicalized source = %q, want exact %q", raw.Source, rawSource)
	}
	if raw.Source == canonical.Source {
		t.Fatal("whitespace-distinct job sources must remain distinct")
	}

	rawFingerprint, err := requestFingerprint(raw)
	if err != nil {
		t.Fatalf("fingerprint raw source: %v", err)
	}
	canonicalFingerprint, err := requestFingerprint(canonical)
	if err != nil {
		t.Fatalf("fingerprint whitespace-free source: %v", err)
	}
	if rawFingerprint == canonicalFingerprint {
		t.Fatal("whitespace-distinct job sources must have distinct request fingerprints")
	}
}

func TestCanonicalizeJobRequestUsesCanonicalRequestID(t *testing.T) {
	hash := strings.Repeat("e", 64)
	req := JobRequest{
		RequestID: "550E8400-E29B-41D4-A716-446655440000",
		Type:      queue.TypeImageThumbnail,
		Hash:      hash,
		Source:    "uploads/" + hash + "-upload-id.png",
	}
	if err := canonicalizeJobRequest(&req); err != nil {
		t.Fatalf("canonicalizeJobRequest returned error: %v", err)
	}
	if req.RequestID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("unexpected canonical request ID %q", req.RequestID)
	}
}
func TestRequestFingerprintIncludesCallbackOnlyForExplicitRequestID(t *testing.T) {
	hash := strings.Repeat("f", 64)
	base := JobRequest{
		Type:    queue.TypeImageThumbnail,
		Hash:    hash,
		Source:  "uploads/" + hash + ".png",
		Options: map[string]any{},
	}
	first := base
	first.CallbackURL = "https://one.example.test/callback"
	second := base
	second.CallbackURL = "https://two.example.test/callback"

	legacyFirst, err := requestFingerprint(first)
	if err != nil {
		t.Fatalf("legacy fingerprint: %v", err)
	}
	legacySecond, err := requestFingerprint(second)
	if err != nil {
		t.Fatalf("legacy fingerprint: %v", err)
	}
	if legacyFirst != legacySecond {
		t.Fatal("legacy processing idempotency must remain callback-independent")
	}

	first.RequestID = "550e8400-e29b-41d4-a716-446655440000"
	second.RequestID = first.RequestID
	tokenFirst, err := requestFingerprint(first)
	if err != nil {
		t.Fatalf("token fingerprint: %v", err)
	}
	tokenSecond, err := requestFingerprint(second)
	if err != nil {
		t.Fatalf("token fingerprint: %v", err)
	}
	if tokenFirst == tokenSecond {
		t.Fatal("same request_id with a different callback must conflict")
	}
}

func TestJobAdmissionRejectsWrongTargetBeforeDatabaseOrQueue(t *testing.T) {
	actual := cleanupTestTarget(t)
	wrong := cleanupTargetFor(t, actual.DeploymentID, "source-b", "media")
	h := &JobHandler{target: actual}
	hash := strings.Repeat("a", 64)
	body, err := json.Marshal(JobRequest{
		Type:             queue.TypeImageThumbnail,
		Hash:             hash,
		Source:           "uploads/" + hash + ".png",
		DeploymentTarget: &wrong,
	})
	if err != nil {
		t.Fatalf("marshal job request: %v", err)
	}
	e := echo.New()
	e.POST("/api/jobs", h.Create)
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d body=%s", resp.Code, resp.Body.String())
	}
	assertDeploymentHeaders(t, resp, actual)
}

func TestRequestFingerprintRemainsCompatibleWhenTargetPreconditionIsAdded(t *testing.T) {
	hash := strings.Repeat("b", 64)
	base := JobRequest{
		RequestID: "550e8400-e29b-41d4-a716-446655440000",
		Type:      queue.TypeImageThumbnail,
		Hash:      hash,
		Source:    "uploads/" + hash + ".png",
		Options:   map[string]any{},
	}
	withTarget := base
	target := cleanupTestTarget(t)
	withTarget.DeploymentTarget = &target

	legacyFingerprint, err := requestFingerprint(base)
	if err != nil {
		t.Fatalf("legacy fingerprint: %v", err)
	}
	v2Fingerprint, err := requestFingerprint(withTarget)
	if err != nil {
		t.Fatalf("v2 fingerprint: %v", err)
	}
	if legacyFingerprint != v2Fingerprint {
		t.Fatal("transport target precondition must not break request_id replay compatibility")
	}
}
func TestSourceSizeForRequestUsesExactWhitespaceDistinctKey(t *testing.T) {
	store := testutil.NewFakeStore()
	sourcePath := "uploads/ video.mp4 "
	if err := store.Put(t.Context(), "source", sourcePath, strings.NewReader("1234567890"), "video/mp4"); err != nil {
		t.Fatalf("put source: %v", err)
	}

	h := &JobHandler{
		sourceStore:    store,
		sourceBucket:   "source",
		largeThreshold: 5,
		maxFileSize:    4,
	}

	_, err := h.sourceSizeForRequest(t.Context(), JobRequest{
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

func TestSourceSizeForRequestRejectsMissingVideoSource(t *testing.T) {
	h := &JobHandler{
		sourceStore:    testutil.NewFakeStore(),
		sourceBucket:   "source",
		largeThreshold: 5,
		maxFileSize:    10,
	}

	_, err := h.sourceSizeForRequest(t.Context(), JobRequest{
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
