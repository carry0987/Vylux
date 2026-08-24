package integration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"Vylux/internal/audio"
	"Vylux/internal/config"
	"Vylux/internal/encryption"
	"Vylux/internal/handler"
	"Vylux/internal/queue"
	handlerspkg "Vylux/internal/queue/handlers"
	"Vylux/internal/storage"

	"github.com/labstack/echo/v5"
)

func TestAudioJob_StructuredProcessCompletesAndPublishesStreamingAssets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, rawStore, queries, _, cleanup := newS3BackedTestServerWithDeps(t)
	defer cleanup()

	ctx := context.Background()
	scratchDir := t.TempDir()
	oldScratchDir := config.ScratchDir
	config.ScratchDir = scratchDir
	t.Cleanup(func() {
		config.ScratchDir = oldScratchDir
	})

	binDir := t.TempDir()
	ffmpegPath, ffprobePath, packagerPath, waveformDataPath := writeAudioToolStubs(t, binDir)
	audio.SetFFmpegPath(ffmpegPath)
	audio.SetFFprobePath(ffprobePath)
	audio.SetPackagerPath(packagerPath)
	t.Setenv("VYLUX_WAVEFORM_DATA", waveformDataPath)

	cfg.WorkerConcurrency = 1
	cfg.LargeWorkerConcurrency = 1
	cfg.WorkerMetricsPort = 0
	cfg.MaxFileSize = 1024 * 1024
	cfg.FFmpegPath = ffmpegPath
	cfg.FFprobePath = ffprobePath
	cfg.ShakaPackagerPath = packagerPath

	mediaStore := storage.WithInstrumentation(rawStore, "media", "s3")
	sourceStore := storage.WithInstrumentation(rawStore, "source", "s3")
	keyWrapper, err := encryption.NewKeyWrapper(cfg.EncryptionKey)
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	queueClient, err := queue.NewClient(cfg.RedisURL)
	if err != nil {
		t.Fatalf("queue client: %v", err)
	}
	defer queueClient.Close()

	workerDeps := &handlerspkg.Deps{
		SourceStore: sourceStore,
		MediaStore:  mediaStore,
		Queries:     queries,
		QueueClient: queueClient,
		Config:      cfg,
		KeyWrapper:  keyWrapper,
	}

	worker, err := queue.NewServer(cfg)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	worker.HandleFunc(queue.TypeAudioTranscode, handlerspkg.HandleAudioTranscode(workerDeps))

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerErrCh := make(chan error, 1)
	go func() {
		workerErrCh <- worker.Run(workerCtx)
	}()

	hash := "a0b1c2d3e4f5a6b7"
	sourceKey := "uploads/demo.flac"
	if err := rawStore.Put(ctx, cfg.SourceBucket, sourceKey, bytes.NewReader([]byte("fake-audio-source")), "audio/flac"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	body := map[string]any{
		"source": map[string]any{
			"hash": hash,
			"key":  sourceKey,
		},
		"pipeline": map[string]any{
			"package": map[string]any{
				"hls": map[string]any{"enabled": true, "profile": "stream_aac_standard"},
			},
			"downloads": []map[string]any{{"profile": "download_mp3_high"}, {"profile": "download_flac_standard"}},
			"waveform":  map[string]any{"enabled": true, "profile": "waveform_standard", "bins": 4},
		},
	}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/audio/jobs", bytes.NewReader(jsonBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/audio/jobs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200-202, got %d: %s", resp.StatusCode, string(body))
	}

	var created handler.JobResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.JobID == nil {
		t.Fatal("expected created job id")
	}

	status := waitForAudioJobStatus(t, ts.URL, cfg.APIKey, *created.JobID)
	if status.Status != "completed" {
		t.Fatalf("expected completed status, got %q with error %q", status.Status, status.Error)
	}

	results, ok := status.Results.(map[string]any)
	if !ok {
		t.Fatalf("results type = %T, want map[string]any", status.Results)
	}
	if _, ok := results["media_kind"]; ok {
		t.Fatalf("expected media_kind to be omitted, got %#v", results["media_kind"])
	}

	stages, ok := results["stages"].(map[string]any)
	if !ok {
		t.Fatalf("stages type = %T, want map[string]any", results["stages"])
	}
	for _, name := range []string{"source", "probe", "package", "downloads", "waveform"} {
		stage, ok := stages[name].(map[string]any)
		if !ok {
			t.Fatalf("stage %s type = %T", name, stages[name])
		}
		if stage["status"] != "ready" {
			t.Fatalf("stage %s status = %#v, want ready", name, stage["status"])
		}
	}

	streaming, ok := results["streaming"].(map[string]any)
	if !ok {
		t.Fatalf("streaming type = %T", results["streaming"])
	}
	if streaming["master_playlist"] != fmt.Sprintf("audio/%s/%s/hls/master.m3u8", hash[:2], hash) {
		t.Fatalf("master_playlist = %#v", streaming["master_playlist"])
	}

	downloads, ok := results["downloads"].([]any)
	if !ok || len(downloads) != 2 {
		t.Fatalf("downloads = %#v, want 2 artifacts", results["downloads"])
	}
	if waveform, ok := results["waveform"].(map[string]any); !ok || waveform["bins"] != float64(4) {
		t.Fatalf("waveform = %#v, want bins=4", results["waveform"])
	}

	for _, key := range []string{
		fmt.Sprintf("audio/%s/%s/hls/master.m3u8", hash[:2], hash),
		fmt.Sprintf("audio/%s/%s/downloads/audio.mp3", hash[:2], hash),
		fmt.Sprintf("audio/%s/%s/downloads/audio.flac", hash[:2], hash),
		fmt.Sprintf("audio/%s/%s/waveform/waveform.json", hash[:2], hash),
	} {
		exists, err := rawStore.Exists(ctx, cfg.MediaBucket, key)
		if err != nil {
			t.Fatalf("exists %s: %v", key, err)
		}
		if !exists {
			t.Fatalf("expected media object %s to exist", key)
		}
	}

	streamResp, err := http.Get(ts.URL + "/stream/" + hash + "/hls/master.m3u8")
	if err != nil {
		t.Fatalf("GET /stream audio master playlist: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(streamResp.Body)
		t.Fatalf("expected 200, got %d: %s", streamResp.StatusCode, string(body))
	}

	cancelWorker()
	select {
	case err := <-workerErrCh:
		if err != nil {
			t.Fatalf("worker run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down")
	}
}

// These tests intentionally hit the retired generic create route to verify it now returns 404.
func TestRetiredJobsCreateRouteReturnsNotFoundForAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	body := map[string]any{
		"media_kind": "audio",
		"operation":  "process",
		"source": map[string]any{
			"hash": "reject-audio-generic-route",
			"key":  "uploads/demo.flac",
		},
	}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(jsonBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/jobs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, string(body))
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !bytes.Contains(bodyText, []byte("Not Found")) {
		t.Fatalf("unexpected response body: %s", string(bodyText))
	}
}

func TestRetiredJobsCreateRouteReturnsNotFoundForVideo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	body := map[string]any{
		"media_kind": "video",
		"operation":  "process",
		"source": map[string]any{
			"hash": "reject-video-generic-route",
			"key":  "uploads/demo.mp4",
		},
	}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(jsonBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/jobs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404, got %d: %s", resp.StatusCode, string(body))
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !bytes.Contains(bodyText, []byte("Not Found")) {
		t.Fatalf("unexpected response body: %s", string(bodyText))
	}
}

func waitForAudioJobStatus(t *testing.T, baseURL, apiKey, jobID string) handler.JobStatusResponse {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/jobs/"+jobID, nil)
		req.Header.Set("X-API-Key", apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/jobs/:id: %v", err)
		}
		var status handler.JobStatusResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if decodeErr != nil {
			t.Fatalf("decode status response: %v", decodeErr)
		}
		if status.Status == "completed" || status.Status == "failed" {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for job terminal state; last status=%q progress=%d", status.Status, status.Progress)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func writeAudioToolStubs(t *testing.T, dir string) (ffmpegPath, ffprobePath, packagerPath, waveformDataPath string) {
	t.Helper()
	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath = filepath.Join(dir, "ffprobe")
	packagerPath = filepath.Join(dir, "packager")
	waveformDataPath = filepath.Join(dir, "waveform.f32")
	writeFloat32Samples(t, waveformDataPath, []float32{0, 0.5, -0.2, 1.0})

	ffmpegScript := "#!/bin/sh\nset -eu\nlast=''\nfor arg in \"$@\"; do last=\"$arg\"; done\nprev=''\nwaveform=0\nfor arg in \"$@\"; do\n  if [ \"$prev\" = '-f' ] && [ \"$arg\" = 'f32le' ]; then waveform=1; fi\n  prev=\"$arg\"\ndone\nif [ \"$waveform\" -eq 1 ] && [ \"$last\" = '-' ]; then\n  cat \"$VYLUX_WAVEFORM_DATA\"\n  exit 0\nfi\ncase \"$last\" in\n  *.mp3|*.flac|*.mp4)\n    mkdir -p \"$(dirname \"$last\")\"\n    printf 'stub-media' > \"$last\"\n    ;;\nesac\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	ffprobeScript := "#!/bin/sh\nprintf '%s' '{\"streams\":[{\"index\":0,\"codec_name\":\"flac\",\"codec_type\":\"audio\",\"sample_rate\":\"48000\",\"channels\":2,\"channel_layout\":\"stereo\",\"bit_rate\":\"1536000\",\"bits_per_sample\":24}],\"format\":{\"format_name\":\"flac\",\"duration\":\"12.5\",\"bit_rate\":\"1536000\"}}'\n"
	if err := os.WriteFile(ffprobePath, []byte(ffprobeScript), 0o755); err != nil {
		t.Fatalf("write ffprobe stub: %v", err)
	}

	packagerScript := "#!/bin/sh\nset -eu\ndescriptor=\"$1\"\nmaster=''\nprev=''\nfor arg in \"$@\"; do\n  if [ \"$prev\" = '--hls_master_playlist_output' ]; then master=\"$arg\"; fi\n  prev=\"$arg\"\ndone\ninit=$(printf '%s' \"$descriptor\" | tr ',' '\\n' | sed -n 's/^init_segment=//p')\nplaylist=$(printf '%s' \"$descriptor\" | tr ',' '\\n' | sed -n 's/^playlist_name=//p')\nsegment_template=$(printf '%s' \"$descriptor\" | tr ',' '\\n' | sed -n 's/^segment_template=//p')\nmkdir -p \"$(dirname \"$master\")\" \"$(dirname \"$init\")\" \"$(dirname \"$playlist\")\" \"$(dirname \"$segment_template\")\"\nprintf '#EXTM3U\\n' > \"$master\"\n: > \"$init\"\nprintf '#EXTM3U\\n' > \"$playlist\"\nsegment=$(printf '%s' \"$segment_template\" | sed 's/\\$Number\\$/1/g')\n: > \"$segment\"\n"
	if err := os.WriteFile(packagerPath, []byte(packagerScript), 0o755); err != nil {
		t.Fatalf("write packager stub: %v", err)
	}

	return ffmpegPath, ffprobePath, packagerPath, waveformDataPath
}

func writeFloat32Samples(t *testing.T, path string, samples []float32) {
	t.Helper()
	buf := new(bytes.Buffer)
	for _, sample := range samples {
		if err := binary.Write(buf, binary.LittleEndian, sample); err != nil {
			t.Fatalf("binary.Write: %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write waveform fixture: %v", err)
	}
}
