package handlers

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"Vylux/internal/config"
	"Vylux/tests/testutil"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWorkerIOHelpersCreateSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	prevProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
	})
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	})
	store := testutil.NewFakeStore()
	oldScratchDir := config.ScratchDir
	config.ScratchDir = t.TempDir()
	t.Cleanup(func() {
		config.ScratchDir = oldScratchDir
	})
	ctx := context.Background()

	if err := store.Put(ctx, "source", "uploads/source.txt", bytes.NewReader([]byte("hello")), "text/plain"); err != nil {
		t.Fatalf("prepare source object: %v", err)
	}

	data, err := fetchSource(ctx, store, "source", "uploads/source.txt")
	if err != nil {
		t.Fatalf("fetchSource: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected fetchSource result: %q", string(data))
	}

	tmpPath, cleanup, err := downloadToTemp(ctx, store, "source", "uploads/source.txt", "trace-test-*")
	if err != nil {
		t.Fatalf("downloadToTemp: %v", err)
	}
	defer cleanup()
	if filepath.Base(tmpPath) == "" {
		t.Fatal("expected temp file path")
	}

	if err := uploadBytes(ctx, store, "media", "images/ab/hash/thumb.webp", "image/webp", []byte("thumb")); err != nil {
		t.Fatalf("uploadBytes: %v", err)
	}

	outDir := t.TempDir()
	masterPath := filepath.Join(outDir, "master.m3u8")
	segmentPath := filepath.Join(outDir, "r720_h264", "seg_000.m4s")
	if err := os.MkdirAll(filepath.Dir(segmentPath), 0o755); err != nil {
		t.Fatalf("mkdir hls dir: %v", err)
	}
	if err := os.WriteFile(masterPath, []byte("#EXTM3U"), 0o644); err != nil {
		t.Fatalf("write master playlist: %v", err)
	}
	if err := os.WriteFile(segmentPath, []byte("segment"), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}

	uploaded, err := uploadHLSDir(ctx, store, "media", "trace-hash", outDir)
	if err != nil {
		t.Fatalf("uploadHLSDir: %v", err)
	}
	if len(uploaded) != 2 {
		t.Fatalf("uploaded key count = %d, want 2", len(uploaded))
	}

	spanNames := map[string]bool{}
	for _, span := range recorder.Ended() {
		spanNames[span.Name()] = true
	}

	for _, want := range []string{
		"worker.fetch.source",
		"worker.download.source",
		"worker.upload.object",
		"worker.upload.hls_dir",
	} {
		if !spanNames[want] {
			t.Fatalf("missing span %q; got %v", want, spanNames)
		}
	}
}

func TestWorkerIOHelpersSetErrorStatus(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	prevProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
	})
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	})

	store := testutil.NewFakeStore()
	_, err := fetchSource(context.Background(), store, "source", "missing.txt")
	if err == nil {
		t.Fatal("expected fetchSource to fail for missing object")
	}

	ended := recorder.Ended()
	if len(ended) == 0 {
		t.Fatal("expected at least one ended span")
	}

	last := ended[len(ended)-1]
	if last.Name() != "worker.fetch.source" {
		t.Fatalf("last span name = %q, want worker.fetch.source", last.Name())
	}
	if last.Status().Code != codes.Error {
		t.Fatal("expected error status on missing source span")
	}
}
