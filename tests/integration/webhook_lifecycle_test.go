package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"Vylux/internal/lifecycle"
	"Vylux/internal/webhook"
)

func TestWebhookRetryIsSuppressedAfterDurableTombstone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, _, _, queries, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()

	firstAttempt := make(chan webhook.CallbackPayload, 1)
	var attempts atomic.Int32
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		var payload webhook.CallbackPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			select {
			case firstAttempt <- payload:
			default:
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer callbackServer.Close()

	hash := strings.Repeat("e", 64)
	source := "uploads/" + hash + "-upload-id.png"
	requestID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	coordinator := lifecycle.NewCoordinator(pool)
	client := webhook.NewClient("test-secret", queries, coordinator)
	delivered := make(chan struct{})
	go func() {
		client.Deliver(context.Background(), requestID, callbackServer.URL, &webhook.CallbackPayload{
			JobID:  requestID,
			Type:   "image:thumbnail",
			Hash:   hash,
			Source: source,
			Status: "completed",
		})
		close(delivered)
	}()

	var payload webhook.CallbackPayload
	select {
	case payload = <-firstAttempt:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook did not make its first delivery attempt")
	}
	if payload.JobID != requestID || payload.Source != source {
		t.Fatalf("webhook did not echo operation identity: %#v", payload)
	}

	if err := coordinator.Fence(t.Context(), hash, source); err != nil {
		t.Fatalf("persist tombstone between webhook attempts: %v", err)
	}

	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook retry did not stop after tombstone")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("tombstoned webhook made %d HTTP attempts, want exactly 1", got)
	}
}
