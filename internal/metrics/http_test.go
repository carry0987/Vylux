package metrics

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewMux_ServesHealthzAndMetrics(t *testing.T) {
	ObserveImageResult("test")

	ts := httptest.NewServer(NewMux())
	defer ts.Close()

	healthResp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer healthResp.Body.Close()

	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", healthResp.StatusCode)
	}
	var healthBody struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(healthResp.Body).Decode(&healthBody); err != nil {
		t.Fatalf("decode /healthz body: %v", err)
	}
	if healthBody.Status != "ok" {
		t.Fatalf("expected status=ok, got %#v", healthBody)
	}

	metricsResp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer metricsResp.Body.Close()

	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", metricsResp.StatusCode)
	}
	metricsBody, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	if !strings.Contains(string(metricsBody), "vylux_image_results_total") {
		t.Fatal("expected /metrics output to include vylux_image_results_total")
	}
}
