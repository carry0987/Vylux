package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Vylux/internal/deployment"

	"github.com/labstack/echo/v5"
)

type strictCleanupCall struct {
	hash   string
	source string
}

type fakeMediaCleaner struct {
	legacyErr   error
	strictErr   error
	hashes      []string
	strictCalls []strictCleanupCall
}

func (c *fakeMediaCleaner) Cleanup(_ context.Context, hash string) error {
	c.hashes = append(c.hashes, hash)
	return c.legacyErr
}

func (c *fakeMediaCleaner) StrictCleanup(_ context.Context, hash, source string) error {
	c.strictCalls = append(c.strictCalls, strictCleanupCall{hash: hash, source: source})
	return c.strictErr
}

func TestCleanupHandlerKeepsLegacyPurgeReusableWithoutGCConfirmation(t *testing.T) {
	cleaner := &fakeMediaCleaner{}
	handler := &CleanupHandler{cleaner: cleaner}
	e := echo.New()
	e.DELETE("/api/media/:hash", handler.Handle)

	req := httptest.NewRequest(http.MethodDelete, "/api/media/missing-hash", nil)
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.Code)
	}
	if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
		t.Fatalf("legacy purge must not carry GC confirmation, got %q", got)
	}
	if len(cleaner.hashes) != 1 || cleaner.hashes[0] != "missing-hash" {
		t.Fatalf("expected cleanup for missing-hash, got %v", cleaner.hashes)
	}
}

func TestCleanupHandlerRejectsIncompleteCleanupWithoutConfirmation(t *testing.T) {
	cleaner := &fakeMediaCleaner{legacyErr: errors.New("storage unavailable")}
	handler := &CleanupHandler{cleaner: cleaner}
	e := echo.New()
	e.DELETE("/api/media/:hash", handler.Handle)

	req := httptest.NewRequest(http.MethodDelete, "/api/media/retry-hash", nil)
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Code)
	}
	if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
		t.Fatalf("incomplete cleanup must not be confirmed, got header %q", got)
	}
}

func TestStrictCleanupHandlerConfirmsOnlyCompleteExactSourceCleanup(t *testing.T) {
	target := cleanupTestTarget(t)
	cleaner := &fakeMediaCleaner{}
	handler := &CleanupHandler{cleaner: cleaner, target: target}
	e := echo.New()
	e.DELETE("/api/media/:hash/strict", handler.HandleStrict)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/media/content-hash/strict",
		strings.NewReader(strictCleanupBody(
			t,
			"uploads/content-hash-upload-id.png",
			target,
		)),
	)
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get(cleanupConfirmedHeader); got != "1" {
		t.Fatalf("expected strict cleanup confirmation, got %q", got)
	}
	want := strictCleanupCall{hash: "content-hash", source: "uploads/content-hash-upload-id.png"}
	assertDeploymentHeaders(t, resp, target)
	if len(cleaner.strictCalls) != 1 || cleaner.strictCalls[0] != want {
		t.Fatalf("unexpected strict cleanup calls: %#v", cleaner.strictCalls)
	}
	if len(cleaner.hashes) != 0 {
		t.Fatalf("strict endpoint must not use legacy cleanup, got %v", cleaner.hashes)
	}
}

func TestStrictCleanupHandlerPreservesWhitespaceDistinctSources(t *testing.T) {
	target := cleanupTestTarget(t)
	cleaner := &fakeMediaCleaner{}
	handler := &CleanupHandler{cleaner: cleaner, target: target}
	e := echo.New()
	e.DELETE("/api/media/:hash/strict", handler.HandleStrict)
	hash := strings.Repeat("a", 64)
	rawSource := "uploads/ " + hash + "-upload-id.png "
	sources := []string{rawSource, "uploads/" + hash + "-upload-id.png"}

	for _, source := range sources {
		req := httptest.NewRequest(
			http.MethodDelete,
			"/api/media/"+hash+"/strict",
			strings.NewReader(strictCleanupBody(t, source, target)),
		)
		resp := httptest.NewRecorder()
		e.ServeHTTP(resp, req)

		if resp.Code != http.StatusNoContent {
			t.Fatalf("source %q: expected 204, got %d body=%s", source, resp.Code, resp.Body.String())
		}
	}

	if len(cleaner.strictCalls) != len(sources) {
		t.Fatalf("expected %d strict calls, got %#v", len(sources), cleaner.strictCalls)
	}
	for index, source := range sources {
		if cleaner.strictCalls[index].source != source {
			t.Fatalf("strict call %d source = %q, want exact %q", index, cleaner.strictCalls[index].source, source)
		}
	}
	if cleaner.strictCalls[0].source == cleaner.strictCalls[1].source {
		t.Fatal("whitespace-distinct object keys must not collapse to one source")
	}
}

func TestStrictCleanupHandlerReturns503WithoutConfirmationWhenIncomplete(t *testing.T) {
	cleaner := &fakeMediaCleaner{strictErr: errors.New("object store unavailable")}
	target := cleanupTestTarget(t)
	handler := &CleanupHandler{cleaner: cleaner, target: target}
	e := echo.New()
	e.DELETE("/api/media/:hash/strict", handler.HandleStrict)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/media/retry-hash/strict",
		strings.NewReader(strictCleanupBody(t, "source/retry-hash.png", target)),
	)
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Code)
	}
	if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
		t.Fatalf("incomplete strict cleanup must not be confirmed, got %q", got)
	}
}

func TestStrictCleanupHandlerRejectsInvalidJSONAndSource(t *testing.T) {
	tests := []string{
		``,
		`{}`,
		`{"source":"   "}`,
		`{"source":"valid","extra":true}`,
		`{"source":"valid"} {}`,
		`{"source":`,
	}

	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			cleaner := &fakeMediaCleaner{}
			handler := &CleanupHandler{cleaner: cleaner}
			e := echo.New()
			e.DELETE("/api/media/:hash/strict", handler.HandleStrict)

			req := httptest.NewRequest(http.MethodDelete, "/api/media/hash/strict", strings.NewReader(body))
			resp := httptest.NewRecorder()
			e.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", resp.Code, resp.Body.String())
			}
			if len(cleaner.strictCalls) != 0 {
				t.Fatalf("invalid request must not call strict cleaner: %#v", cleaner.strictCalls)
			}
			if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
				t.Fatalf("invalid request must not carry confirmation, got %q", got)
			}
		})
	}
}

func TestCleanupHandlerRejectsMissingHash(t *testing.T) {
	cleaner := &fakeMediaCleaner{}
	handler := &CleanupHandler{cleaner: cleaner}
	e := echo.New()

	req := httptest.NewRequest(http.MethodDelete, "/api/media/", nil)
	resp := httptest.NewRecorder()
	ctx := e.NewContext(req, resp)
	err := handler.Handle(ctx)

	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected missing hash to return 400, got %v", err)
	}
	if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
		t.Fatalf("missing hash must not carry confirmation, got %q", got)
	}
	if len(cleaner.hashes) != 0 {
		t.Fatalf("missing hash must not call cleaner, got %v", cleaner.hashes)
	}
}

func TestStrictCleanupHandlerRejectsMissingHash(t *testing.T) {
	cleaner := &fakeMediaCleaner{}
	handler := &CleanupHandler{cleaner: cleaner}
	e := echo.New()

	req := httptest.NewRequest(http.MethodDelete, "/api/media//strict", strings.NewReader(`{"source":"source"}`))
	resp := httptest.NewRecorder()
	ctx := e.NewContext(req, resp)
	err := handler.HandleStrict(ctx)

	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected missing hash to return 400, got %v", err)
	}
	if len(cleaner.strictCalls) != 0 {
		t.Fatalf("missing hash must not call strict cleaner: %#v", cleaner.strictCalls)
	}
	if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
		t.Fatalf("missing hash must not carry confirmation, got %q", got)
	}
}

func TestStrictCleanupHandlerRejectsWrongDeploymentBeforeCleaner(t *testing.T) {
	actual := cleanupTestTarget(t)
	tests := []struct {
		name     string
		expected deployment.Target
	}{
		{name: "wrong deployment", expected: cleanupTargetFor(t, "550e8400-e29b-41d4-a716-446655440099", "source", "media")},
		{name: "wrong source backend", expected: cleanupTargetFor(t, actual.DeploymentID, "source-b", "media")},
		{name: "wrong media backend", expected: cleanupTargetFor(t, actual.DeploymentID, "source", "media-b")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaner := &fakeMediaCleaner{}
			handler := &CleanupHandler{cleaner: cleaner, target: actual}
			e := echo.New()
			e.DELETE("/api/media/:hash/strict", handler.HandleStrict)

			req := httptest.NewRequest(
				http.MethodDelete,
				"/api/media/hash/strict",
				strings.NewReader(strictCleanupBody(t, "source/hash.png", tt.expected)),
			)
			resp := httptest.NewRecorder()
			e.ServeHTTP(resp, req)

			if resp.Code != http.StatusPreconditionFailed {
				t.Fatalf("expected 412, got %d body=%s", resp.Code, resp.Body.String())
			}
			if len(cleaner.strictCalls) != 0 || len(cleaner.hashes) != 0 {
				t.Fatalf("target mismatch reached cleaner before Fence: %#v %#v", cleaner.strictCalls, cleaner.hashes)
			}
			if got := resp.Header().Get(cleanupConfirmedHeader); got != "" {
				t.Fatalf("target mismatch must not be confirmed, got %q", got)
			}
			assertDeploymentHeaders(t, resp, actual)
		})
	}
}

func cleanupTestTarget(t *testing.T) deployment.Target {
	t.Helper()
	return cleanupTargetFor(t, "550e8400-e29b-41d4-a716-446655440000", "source", "media")
}

func cleanupTargetFor(t *testing.T, deploymentID, sourceBucket, mediaBucket string) deployment.Target {
	t.Helper()
	target, err := deployment.NewTarget(
		deploymentID,
		deployment.BackendConfig{
			ProviderKind: "s3",
			Endpoint:     "https://source.example.test",
			Region:       "test",
			Bucket:       sourceBucket,
		},
		deployment.BackendConfig{
			ProviderKind: "r2",
			Endpoint:     "https://media.example.test",
			Region:       "auto",
			Bucket:       mediaBucket,
		},
	)
	if err != nil {
		t.Fatalf("build cleanup target: %v", err)
	}
	return target
}

func strictCleanupBody(t *testing.T, source string, target deployment.Target) string {
	t.Helper()
	body, err := json.Marshal(strictCleanupRequest{
		Source:                source,
		ProtocolVersion:       target.ProtocolVersion,
		DeploymentID:          target.DeploymentID,
		SourceBackendIdentity: target.SourceBackendIdentity,
		MediaBackendIdentity:  target.MediaBackendIdentity,
	})
	if err != nil {
		t.Fatalf("marshal strict cleanup request: %v", err)
	}
	return string(body)
}

func assertDeploymentHeaders(t *testing.T, resp *httptest.ResponseRecorder, target deployment.Target) {
	t.Helper()
	want := map[string]string{
		deployment.HeaderProtocolVersion:       "2",
		deployment.HeaderDeploymentID:          target.DeploymentID,
		deployment.HeaderSourceBackendIdentity: target.SourceBackendIdentity,
		deployment.HeaderMediaBackendIdentity:  target.MediaBackendIdentity,
	}
	for name, expected := range want {
		if got := resp.Header().Get(name); got != expected {
			t.Fatalf("%s = %q, want %q", name, got, expected)
		}
	}
}
