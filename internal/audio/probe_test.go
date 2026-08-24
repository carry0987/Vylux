package audio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSourceFormat(t *testing.T) {
	tests := []struct {
		path string
		want SourceFormat
	}{
		{path: "track.wav", want: FormatWAV},
		{path: "track.flac", want: FormatFLAC},
		{path: "track.aiff", want: FormatAIFF},
		{path: "track.alac", want: FormatALAC},
		{path: "track.mp3", want: FormatMP3},
		{path: "track.m4a", want: FormatM4A},
		{path: "track.opus", want: FormatOpus},
		{path: "track.bin", want: FormatUnknown},
	}

	for _, tc := range tests {
		if got := DetectSourceFormat(tc.path); got != tc.want {
			t.Fatalf("DetectSourceFormat(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestProbeParsesAudioMetadata(t *testing.T) {
	json := `{"streams":[{"index":0,"codec_name":"flac","codec_type":"audio","sample_rate":"48000","channels":2,"channel_layout":"stereo","bit_rate":"1536000","bits_per_sample":24,"tags":{"language":"und"}},{"index":1,"codec_name":"mjpeg","codec_type":"video"}],"format":{"format_name":"flac","duration":"218.3","bit_rate":"1536000","tags":{"title":"Example"}}}`
	scriptPath := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\nprintf '%s' \"$VYLUX_AUDIO_PROBE_JSON\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffprobe stub: %v", err)
	}

	oldFFprobePath := ffprobePath
	ffprobePath = scriptPath
	t.Cleanup(func() {
		ffprobePath = oldFFprobePath
	})
	t.Setenv("VYLUX_AUDIO_PROBE_JSON", json)

	result, err := Probe(context.Background(), "/tmp/example.flac")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.SourceFormat != FormatFLAC {
		t.Fatalf("source format = %q, want %q", result.SourceFormat, FormatFLAC)
	}
	if result.Container != "flac" {
		t.Fatalf("container = %q, want flac", result.Container)
	}
	if result.DurationSec != 218.3 {
		t.Fatalf("duration = %v, want 218.3", result.DurationSec)
	}
	if result.Bitrate != 1536000 {
		t.Fatalf("bitrate = %d, want 1536000", result.Bitrate)
	}
	if len(result.Streams) != 1 {
		t.Fatalf("stream count = %d, want 1", len(result.Streams))
	}
	stream := result.Streams[0]
	if stream.Codec != "flac" || stream.SampleRate != 48000 || stream.Channels != 2 {
		t.Fatalf("unexpected stream metadata: %+v", stream)
	}
	if stream.Tags["language"] != "und" {
		t.Fatalf("stream language = %q, want und", stream.Tags["language"])
	}
	if result.Tags["title"] != "Example" {
		t.Fatalf("title = %q, want Example", result.Tags["title"])
	}
}

func TestProbeFallsBackToStreamCodecForUnknownExtension(t *testing.T) {
	json := `{"streams":[{"index":0,"codec_name":"alac","codec_type":"audio","sample_rate":"44100","channels":2}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"10.5"}}`
	scriptPath := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\nprintf '%s' \"$VYLUX_AUDIO_PROBE_JSON\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffprobe stub: %v", err)
	}

	oldFFprobePath := ffprobePath
	ffprobePath = scriptPath
	t.Cleanup(func() {
		ffprobePath = oldFFprobePath
	})
	t.Setenv("VYLUX_AUDIO_PROBE_JSON", json)

	result, err := Probe(context.Background(), "/tmp/source.bin")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.SourceFormat != FormatALAC {
		t.Fatalf("source format = %q, want %q", result.SourceFormat, FormatALAC)
	}
}

func TestProbeRejectsMissingAudioStreams(t *testing.T) {
	json := `{"streams":[{"index":0,"codec_name":"h264","codec_type":"video"}],"format":{"format_name":"mp4"}}`
	scriptPath := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\nprintf '%s' \"$VYLUX_AUDIO_PROBE_JSON\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffprobe stub: %v", err)
	}

	oldFFprobePath := ffprobePath
	ffprobePath = scriptPath
	t.Cleanup(func() {
		ffprobePath = oldFFprobePath
	})
	t.Setenv("VYLUX_AUDIO_PROBE_JSON", json)

	_, err := Probe(context.Background(), "/tmp/source.mp4")
	if err == nil {
		t.Fatal("expected missing audio stream error")
	}
}
