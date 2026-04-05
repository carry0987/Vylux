package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"Vylux/internal/config"
	"Vylux/internal/handler"
	"Vylux/internal/storage"
	"net/http/httptest"
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

		body, _ := io.ReadAll(resp.Body)
		if string(body) != "OK" {
			t.Errorf("GET %s: expected OK, got %q", endpoint, string(body))
		}
	}
}

// TestJobCreate_Unauthorized verifies that creating a job without API key returns 401.
func TestJobCreate_Unauthorized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `{"type":"image:thumbnail","hash":"abc123","source":"test.jpg","callback_url":"http://example.com/cb"}`
	resp, err := http.Post(ts.URL+"/api/jobs", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /api/jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestJobCreate_Success verifies that creating a job with a valid API key succeeds.
func TestJobCreate_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	body := handler.JobRequest{
		Type:        "image:thumbnail",
		Hash:        "abc123def456",
		Source:      "uploads/test.jpg",
		CallbackURL: "http://example.com/callback",
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/jobs: %v", err)
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

	if result.Hash != body.Hash {
		t.Errorf("expected hash %q, got %q", body.Hash, result.Hash)
	}

	if result.Status != "queued" && result.Status != "completed" {
		t.Errorf("expected status queued or completed, got %q", result.Status)
	}
}

// TestJobGetStatus verifies GET /api/jobs/:id returns job details.
func TestJobGetStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	body := handler.JobRequest{
		Type:        "image:thumbnail",
		Hash:        "status-test-hash",
		Source:      "uploads/test.jpg",
		CallbackURL: "http://example.com/callback",
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(jsonBody))
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

	if statusResult.Hash != body.Hash {
		t.Errorf("expected hash %q, got %q", body.Hash, statusResult.Hash)
	}
}
