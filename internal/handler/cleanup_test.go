package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"Vylux/internal/cache"
	"Vylux/internal/cleanup"
	"Vylux/internal/db/dbq"

	"github.com/labstack/echo/v5"
)

type cleanupFailStore struct{}

func (cleanupFailStore) Get(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (cleanupFailStore) Put(context.Context, string, string, io.Reader, string) error {
	return errors.New("unused")
}
func (cleanupFailStore) Exists(context.Context, string, string) (bool, error) { return false, nil }
func (cleanupFailStore) Size(context.Context, string, string) (int64, error)  { return 0, nil }
func (cleanupFailStore) Delete(context.Context, string, string) error         { return nil }
func (cleanupFailStore) List(context.Context, string, string) ([]string, error) {
	return nil, errors.New("backend unavailable")
}
func (cleanupFailStore) HeadBucket(context.Context, string) error { return nil }

type cleanupHandlerQueries struct{}

func (cleanupHandlerQueries) ListJobsByHash(context.Context, string) ([]dbq.Job, error) {
	return nil, nil
}
func (cleanupHandlerQueries) DeleteStreamEncryptionKeysBySourceHash(context.Context, string) error {
	return nil
}
func (cleanupHandlerQueries) DeleteJobsByHash(context.Context, string) error { return nil }
func (cleanupHandlerQueries) ListImageCacheEntriesByHash(context.Context, string) ([]dbq.ImageCacheEntry, error) {
	return nil, nil
}
func (cleanupHandlerQueries) DeleteImageCacheEntriesByHash(context.Context, string) error { return nil }

func TestCleanupHandlerReturnsRetryableFailureWhenCleanupIncomplete(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/media/hash123", nil)
	resp := httptest.NewRecorder()

	h := &CleanupHandler{cleaner: cleanup.NewCleaner(cleanupFailStore{}, cache.New(1024), cleanupHandlerQueries{}, nil, "media")}
	e.DELETE("/api/media/:hash", h.Handle)
	e.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}

	var body cleanupFailureResponse
	if err := json.NewDecoder(bytes.NewReader(resp.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Retryable {
		t.Fatal("expected retryable=true")
	}
	if len(body.FailedStages) == 0 {
		t.Fatalf("expected failed stages, got %#v", body)
	}
}
