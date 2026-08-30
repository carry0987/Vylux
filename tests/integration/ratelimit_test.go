package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAudioJobRateLimitReturnsJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, store, cleanup := newTestServerWithStore(t)
	defer cleanup()

	if err := store.Put(t.Context(), cfg.SourceBucket, "uploads/rate-limit.flac", bytes.NewReader([]byte("fake-audio-source")), "audio/flac"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	body := map[string]any{
		"source": map[string]any{
			"hash": "rate-limit-audio-hash",
			"key":  "uploads/rate-limit.flac",
		},
		"pipeline": map[string]any{
			"package": map[string]any{
				"hls": map[string]any{"enabled": true, "profile": "stream_aac_standard"},
			},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	for attempt := 1; attempt <= 31; attempt++ {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/audio/jobs", bytes.NewReader(jsonBody))
		if err != nil {
			t.Fatalf("build request %d: %v", attempt, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", cfg.APIKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /api/audio/jobs attempt %d: %v", attempt, err)
		}

		if attempt < 31 {
			if resp.StatusCode == http.StatusTooManyRequests {
				resp.Body.Close()
				t.Fatalf("unexpected early rate limit on attempt %d", attempt)
			}
			resp.Body.Close()
			continue
		}

		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on attempt %d, got %d", attempt, resp.StatusCode)
		}
		if got := resp.Header.Get("Retry-After"); got != "60" {
			t.Fatalf("expected Retry-After=60, got %q", got)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("expected JSON content type, got %q", got)
		}

		var errorBody struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errorBody); err != nil {
			t.Fatalf("decode 429 response: %v", err)
		}
		if errorBody.Message != "Too Many Requests" {
			t.Fatalf("expected rate limit message, got %#v", errorBody)
		}
	}
}
