package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateWaveformBinsPeaks(t *testing.T) {
	tmpDir := t.TempDir()
	dataPath := filepath.Join(tmpDir, "samples.f32")
	writeFloat32Samples(t, dataPath, []float32{0, 0.5, -0.2, 1.0, -0.8, 0.3, 0.1, -0.4})

	scriptPath := filepath.Join(tmpDir, "ffmpeg")
	script := "#!/bin/sh\ncat \"$VYLUX_WAVEFORM_DATA\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	ffmpegPath = scriptPath
	t.Cleanup(func() { ffmpegPath = oldFFmpegPath })
	t.Setenv("VYLUX_WAVEFORM_DATA", dataPath)

	result, err := GenerateWaveform(context.Background(), "/tmp/input.flac", 4)
	if err != nil {
		t.Fatalf("GenerateWaveform: %v", err)
	}
	if len(result.Bins) != 4 {
		t.Fatalf("bin count = %d, want 4", len(result.Bins))
	}
	want := []float32{0.5, 1.0, 0.8, 0.4}
	for i := range want {
		if math.Abs(float64(result.Bins[i]-want[i])) > 0.0001 {
			t.Fatalf("bin[%d] = %v, want %v", i, result.Bins[i], want[i])
		}
	}
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
		t.Fatalf("WriteFile: %v", err)
	}
}
