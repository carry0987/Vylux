package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"Vylux/internal/config"
	"Vylux/internal/handler"
	"Vylux/internal/storage"
)

// newTestServerWithStore spins up a RustFS-backed test server and returns the raw store for seeding fixtures.
func newTestServerWithStore(t *testing.T) (*httptest.Server, *config.Config, storage.Storage, func()) {
	return newS3BackedTestServer(t)
}

// newTestServer spins up PG + Redis containers, runs migrations, and returns
// an httptest.Server backed by the real Vylux Echo router.
func newTestServer(t *testing.T) (*httptest.Server, *config.Config, func()) {
	ts, cfg, _, cleanup := newTestServerWithStore(t)
	return ts, cfg, cleanup
}

// TestHealthEndpoints verifies /healthz and /readyz return 200.
func TestHealthEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	for _, endpoint := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + endpoint)
		if err != nil {
			t.Fatalf("GET %s: %v", endpoint, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d", endpoint, resp.StatusCode)
		}

		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s body: %v", endpoint, err)
		}
		if body.Status != "ok" {
			t.Errorf("GET %s: expected status=ok, got %#v", endpoint, body)
		}
	}
}

// TestAudioJobCreate_Unauthorized verifies that audio job creation requires authentication.
func TestAudioJobCreate_Unauthorized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `{"source":{"hash":"abc123","key":"uploads/audio.flac"}}`
	resp, err := http.Post(ts.URL+"/api/audio/jobs", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /api/audio/jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertJSONErrorResponse(t, resp, "missing API key")
}

// TestVideoJobCreate_Success verifies that video job creation succeeds on the domain route.
func TestVideoJobCreate_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, store, cleanup := newTestServerWithStore(t)
	defer cleanup()
	if err := store.Put(t.Context(), cfg.SourceBucket, "uploads/test.mp4", bytes.NewReader([]byte("video")), "video/mp4"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	body := map[string]any{
		"source": map[string]any{
			"hash": "abc123def456",
			"key":  "uploads/test.mp4",
		},
		"pipeline": map[string]any{
			"package": map[string]any{
				"hls": map[string]any{"enabled": true, "profile": "stream_video_standard"},
			},
		},
		"delivery": map[string]any{
			"callback_url": "http://example.com/callback",
		},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/video/jobs", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/video/jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200-202, got %d: %s", resp.StatusCode, string(respBody))
	}

	var result handler.JobResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if result.Hash != "abc123def456" {
		t.Errorf("expected hash %q, got %q", "abc123def456", result.Hash)
	}

	if result.Status != "queued" && result.Status != "completed" {
		t.Errorf("expected status queued or completed, got %q", result.Status)
	}
}

// TestJobGetStatus verifies the shared GET /api/jobs/:id lifecycle endpoint returns job details.
func TestJobGetStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, store, cleanup := newTestServerWithStore(t)
	defer cleanup()
	if err := store.Put(t.Context(), cfg.SourceBucket, "uploads/status.mp4", bytes.NewReader([]byte("video")), "video/mp4"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	body := map[string]any{
		"source": map[string]any{
			"hash": "status-test-hash",
			"key":  "uploads/status.mp4",
		},
		"pipeline": map[string]any{
			"package": map[string]any{
				"hls": map[string]any{"enabled": true, "profile": "stream_video_standard"},
			},
		},
		"delivery": map[string]any{
			"callback_url": "http://example.com/callback",
		},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/video/jobs", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	defer resp.Body.Close()

	var createResult handler.JobResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResult); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	if createResult.JobID == nil {
		t.Fatal("expected non-nil job_id")
	}

	statusReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/jobs/"+*createResult.JobID, nil)
	statusReq.Header.Set("X-API-Key", cfg.APIKey)

	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("GET /api/jobs/:id: %v", err)
	}
	defer statusResp.Body.Close()

	if statusResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", statusResp.StatusCode)
	}

	var statusResult handler.JobStatusResponse
	if err := json.NewDecoder(statusResp.Body).Decode(&statusResult); err != nil {
		t.Fatalf("decode status response: %v", err)
	}

	if statusResult.Hash != "status-test-hash" {
		t.Errorf("expected hash %q, got %q", "status-test-hash", statusResult.Hash)
	}
}
