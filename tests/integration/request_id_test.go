package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"Vylux/internal/handler"
	"Vylux/internal/queue"
)

func TestJobRequestIDIsDurableIdempotencyAndTaskToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	hash := strings.Repeat("d", 64)
	requestID := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	requestBody := handler.JobRequest{
		RequestID:   requestID,
		Type:        queue.TypeImageThumbnail,
		Hash:        hash,
		Source:      "uploads/" + hash + "-upload-id.png",
		CallbackURL: "https://host.example.test/api/vylux/webhook",
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal job request: %v", err)
	}

	newRequest := func(body []byte) *http.Request {
		req, requestErr := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(body))
		if requestErr != nil {
			t.Fatalf("create job request: %v", requestErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", cfg.APIKey)
		return req
	}
	post := func(body []byte) (*http.Response, handler.JobResponse) {
		resp, postErr := http.DefaultClient.Do(newRequest(body))
		if postErr != nil {
			t.Fatalf("POST /api/jobs: %v", postErr)
		}
		var result handler.JobResponse
		if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
			if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
				resp.Body.Close()
				t.Fatalf("decode job response: %v", decodeErr)
			}
		}
		return resp, result
	}

	first, firstResult := post(encoded)
	first.Body.Close()
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first request status = %d, want 202", first.StatusCode)
	}
	if firstResult.JobID == nil || *firstResult.JobID != requestID {
		t.Fatalf("job_id = %#v, want request_id %q", firstResult.JobID, requestID)
	}

	inspector, err := queue.NewInspector(cfg.RedisURL)
	if err != nil {
		t.Fatalf("create queue inspector: %v", err)
	}
	defer inspector.Close()
	info, err := inspector.GetTaskInfo(queue.QueueCritical, requestID)
	if err != nil {
		t.Fatalf("inspect explicit task ID: %v", err)
	}
	if info.ID != requestID {
		t.Fatalf("Asynq task ID = %q, want request_id %q", info.ID, requestID)
	}
	if err := inspector.DeleteTask(queue.QueueCritical, requestID); err != nil {
		t.Fatalf("remove queued task to simulate ambiguous enqueue loss: %v", err)
	}

	replayed, replayResult := post(encoded)
	replayed.Body.Close()
	if replayed.StatusCode != http.StatusOK {
		t.Fatalf("idempotent replay status = %d, want 200", replayed.StatusCode)
	}
	if replayResult.JobID == nil || *replayResult.JobID != requestID {
		t.Fatalf("replayed job_id = %#v, want %q", replayResult.JobID, requestID)
	}
	restored, err := inspector.GetTaskInfo(queue.QueueCritical, requestID)
	if err != nil {
		t.Fatalf("idempotent replay did not restore missing queued task: %v", err)
	}
	if restored.ID != requestID {
		t.Fatalf("restored Asynq task ID = %q, want request_id %q", restored.ID, requestID)
	}

	conflictBody := requestBody
	conflictBody.CallbackURL = "https://other.example.test/api/vylux/webhook"
	conflictEncoded, err := json.Marshal(conflictBody)
	if err != nil {
		t.Fatalf("marshal conflicting request: %v", err)
	}
	conflict, _ := post(conflictEncoded)
	defer conflict.Body.Close()
	if conflict.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(conflict.Body)
		t.Fatalf("request_id conflict status = %d, want 409: %s", conflict.StatusCode, body)
	}

	statusRequest, err := http.NewRequest(http.MethodGet, ts.URL+"/api/jobs/"+requestID, nil)
	if err != nil {
		t.Fatalf("create status request: %v", err)
	}
	statusRequest.Header.Set("X-API-Key", cfg.APIKey)
	statusResponse, err := http.DefaultClient.Do(statusRequest)
	if err != nil {
		t.Fatalf("GET /api/jobs/:id: %v", err)
	}
	defer statusResponse.Body.Close()
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("status lookup = %d, want 200", statusResponse.StatusCode)
	}
	var status handler.JobStatusResponse
	if err := json.NewDecoder(statusResponse.Body).Decode(&status); err != nil {
		t.Fatalf("decode job status: %v", err)
	}
	if status.JobID != requestID || status.Hash != hash || status.Status != "queued" {
		t.Fatalf("unexpected durable intent: %#v", status)
	}
}
