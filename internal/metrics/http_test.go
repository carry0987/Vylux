package metrics

import (
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
	healthBody, err := io.ReadAll(healthResp.Body)
	if err != nil {
		t.Fatalf("read /healthz body: %v", err)
	}
	if string(healthBody) != "OK" {
		t.Fatalf("expected OK body, got %q", string(healthBody))
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
