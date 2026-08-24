package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"Vylux/internal/signature"
)

func TestMetricsEndpoint_ExposesPrometheusMetrics(t *testing.T) {
	ts, cfg, store, cleanup := newTestServerWithStore(t)
	defer cleanup()

	ctx := context.Background()
	sourceKey := "metrics/sample image.png"
	if err := store.Put(ctx, cfg.SourceBucket, sourceKey, bytes.NewReader(buildTestPNG(t)), "image/png"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	requestSource := strings.ReplaceAll(url.PathEscape(sourceKey), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	resp.Body.Close()

	jobBody, err := json.Marshal(map[string]any{
		"source": map[string]any{
			"hash": "metrics-job-hash",
			"key":  sourceKey,
		},
		"pipeline": map[string]any{
			"package": map[string]any{
				"hls": map[string]any{"enabled": true, "profile": "stream_aac_standard"},
			},
		},
		"delivery": map[string]any{
			"callback_url": "http://example.com/callback",
		},
	})
	if err != nil {
		t.Fatalf("marshal job body: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/audio/jobs", bytes.NewReader(jobBody))
	if err != nil {
		t.Fatalf("new POST /api/audio/jobs request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/audio/jobs: %v", err)
	}
	resp.Body.Close()

	metricsResp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer metricsResp.Body.Close()

	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", metricsResp.StatusCode)
	}

	body, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	out := string(body)

	checks := []string{
		"vylux_http_requests_total",
		"vylux_http_request_duration_seconds",
		"vylux_image_cache_events_total",
		"vylux_image_results_total",
		"vylux_queue_tasks",
		"route=\"/healthz\"",
		"route=\"/img/:sig/:opts/*\"",
		"route=\"/api/audio/jobs\"",
		"layer=\"memory\",result=\"miss\"",
		"layer=\"storage\",result=\"miss\"",
		"queue=\"default\",state=\"pending\"",
	}

	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("expected /metrics output to contain %q", want)
		}
	}
}
