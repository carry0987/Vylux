package video

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTranscodeVariantsPreservesAspectRatioForPortraitInput(t *testing.T) {
	variants := resolveTranscodeVariants(H264OnlyVariants(), 1080, 1920)
	if len(variants) != 5 {
		t.Fatalf("expected 5 variants, got %d", len(variants))
	}

	assertVariant(t, variants[0], "r1080_h264", 608, 1080)
	assertVariant(t, variants[1], "r720_h264", 406, 720)
	assertVariant(t, variants[2], "r480_h264", 270, 480)
	assertVariant(t, variants[3], "r360_h264", 202, 360)
	assertVariant(t, variants[4], "r240_h264", 136, 240)
	if got := estimateBandwidth(variants[0]); got != 5_000_000 {
		t.Fatalf("expected portrait 1080 rung bandwidth 5000000, got %d", got)
	}
}

func TestResolveTranscodeVariantsKeepsMultipleLowerRungsForSmallLandscapeInput(t *testing.T) {
	variants := resolveTranscodeVariants(H264OnlyVariants(), 640, 360)
	if len(variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(variants))
	}
	assertVariant(t, variants[0], "r360_h264", 640, 360)
	assertVariant(t, variants[1], "r240_h264", 426, 240)
}

func TestResolveTranscodeVariantsFallsBackToSmallestRungWithoutUpscaling(t *testing.T) {
	variants := resolveTranscodeVariants(H264OnlyVariants(), 320, 180)
	if len(variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(variants))
	}

	assertVariant(t, variants[0], "r240_h264", 320, 180)
	if got := estimateBandwidth(variants[0]); got != 200_000 {
		t.Fatalf("expected sub-240 fallback bandwidth 200000, got %d", got)
	}
}

func TestProbeVideoGeometryHonorsRotationMetadata(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\nprintf '%s' '{\"streams\":[{\"width\":1920,\"height\":1080,\"side_data_list\":[{\"rotation\":-90}]}]}'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffprobe stub: %v", err)
	}

	oldFFprobePath := ffprobePath
	ffprobePath = scriptPath
	t.Cleanup(func() {
		ffprobePath = oldFFprobePath
	})

	width, height, err := probeVideoGeometry(context.Background(), "/tmp/input.mp4")
	if err != nil {
		t.Fatalf("probeVideoGeometry: %v", err)
	}
	if width != 1080 || height != 1920 {
		t.Fatalf("expected rotated display dimensions 1080x1920, got %dx%d", width, height)
	}
}

func TestPackageHLSBuildsSplitTrackEncryptedArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "packager-args.txt")
	scriptPath := filepath.Join(t.TempDir(), "packager")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$VYLUX_PACKAGER_ARGS\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write packager stub: %v", err)
	}

	oldPackagerPath := packagerPath
	packagerPath = scriptPath
	t.Cleanup(func() {
		packagerPath = oldPackagerPath
	})
	t.Setenv("VYLUX_PACKAGER_ARGS", argsFile)

	variant := TranscodeVariant{
		Label:  "r720_h264",
		Codec:  CodecH264,
		Width:  1280,
		Height: 720,
		CRF:    23,
		ABitR:  "96k",
	}
	keyID := "00112233445566778899aabbccddeeff"
	key := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	if err := packageHLS(context.Background(), t.TempDir(), "/tmp/audio.mp4", map[string]string{
		variant.Label: "/tmp/r720_h264.mp4",
	}, TranscodeOptions{
		Variants:   []TranscodeVariant{variant},
		AudioTrack: DefaultAudioTrack(),
		SegmentSec: 4,
		Encryption: &EncryptionConfig{
			KeyID:     keyID,
			Key:       key,
			HLSKeyURI: "https://media.example.com/api/key/hash123",
		},
	}); err != nil {
		t.Fatalf("packageHLS: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")

	assertArgContains(t, args, "stream=audio")
	assertArgContains(t, args, "playlist_name=audio/und_aac_2ch/playlist.m3u8")
	assertArgContains(t, args, "segment_template=")
	assertArgContains(t, args, "stream=video")
	assertArgContains(t, args, "playlist_name=video/r720_h264/playlist.m3u8")
	assertArgContains(t, args, "bw=2500000")
	assertArgsContainPair(t, args, "--hls_playlist_type", "VOD")
	assertArgsContainPair(t, args, "--segment_duration", "4")
	assertArgsContainPair(t, args, "--fragment_duration", "4")
	assertArgDoesNotContain(t, args, "lang=und")
	assertArgMissing(t, args, "--default_language")
	assertArgEquals(t, args, "--enable_raw_key_encryption")
	assertArgsContainPair(t, args, "--protection_scheme", "cbcs")
	assertArgsContainPair(t, args, "--keys", "label=:key_id="+keyID+":key="+keyHex(key))
	assertArgsContainPair(t, args, "--hls_key_uri", "https://media.example.com/api/key/hash123")
}

func TestPackageHLSIncludesExplicitLanguage(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "packager-args.txt")
	scriptPath := filepath.Join(t.TempDir(), "packager")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$VYLUX_PACKAGER_ARGS\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write packager stub: %v", err)
	}

	oldPackagerPath := packagerPath
	packagerPath = scriptPath
	t.Cleanup(func() {
		packagerPath = oldPackagerPath
	})
	t.Setenv("VYLUX_PACKAGER_ARGS", argsFile)

	track := DefaultAudioTrack()
	track.Language = "en"
	variant := H264OnlyVariants()[0]

	if err := packageHLS(context.Background(), t.TempDir(), "/tmp/audio.mp4", map[string]string{
		variant.Label: "/tmp/r1080_h264.mp4",
	}, TranscodeOptions{
		Variants:   []TranscodeVariant{variant},
		AudioTrack: track,
		SegmentSec: 6,
	}); err != nil {
		t.Fatalf("packageHLS: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")

	assertArgContains(t, args, "lang=en")
	assertArgsContainPair(t, args, "--default_language", "en")
}

func TestPackageHLSRejectsIncompleteEncryptionConfig(t *testing.T) {
	err := packageHLS(context.Background(), t.TempDir(), "", map[string]string{
		"r720_h264": "/tmp/r720_h264.mp4",
	}, TranscodeOptions{
		Variants: []TranscodeVariant{{
			Label:  "r720_h264",
			Codec:  CodecH264,
			Width:  1280,
			Height: 720,
			CRF:    23,
		}},
		Encryption: &EncryptionConfig{KeyID: "0011"},
	})
	if err == nil {
		t.Fatal("expected incomplete encryption config error")
	}
	if !strings.Contains(err.Error(), "incomplete encryption config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEncodeVideoTracksSharesDecodePerCodecFamily(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "ffmpeg-args.txt")
	scriptPath := filepath.Join(tmpDir, "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$VYLUX_FFMPEG_ARGS\"\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    *.mp4)\n      mkdir -p \"$(dirname \"$arg\")\"\n      : > \"$arg\"\n      ;;\n  esac\ndone\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	ffmpegPath = scriptPath
	t.Cleanup(func() {
		ffmpegPath = oldFFmpegPath
	})
	t.Setenv("VYLUX_FFMPEG_ARGS", argsFile)

	variants := []TranscodeVariant{
		{Label: "r1080_h264", Codec: CodecH264, Width: 1920, Height: 1080, CRF: 22},
		{Label: "r720_h264", Codec: CodecH264, Width: 1280, Height: 720, CRF: 23},
		{Label: "r480_h264", Codec: CodecH264, Width: 854, Height: 480, CRF: 24},
	}

	if err := encodeVideoTracks(context.Background(), "/tmp/input.mp4", tmpDir, variants); err != nil {
		t.Fatalf("encodeVideoTracks: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")

	assertArgsContainPair(t, args, "-filter_complex", "[0:v:0]split=3[vsrc0][vsrc1][vsrc2];[vsrc0]scale=1920:1080,setsar=1[vout0];[vsrc1]scale=1280:720,setsar=1[vout1];[vsrc2]scale=854:480,setsar=1[vout2]")
	assertArgOccurrences(t, args, "-i", 1)
	assertArgOccurrences(t, args, "-map", 3)
	assertArgOccurrences(t, args, "-c:v", 3)
	assertArgContains(t, args, filepath.Join(tmpDir, "r1080_h264.mp4"))
	assertArgContains(t, args, filepath.Join(tmpDir, "r720_h264.mp4"))
	assertArgContains(t, args, filepath.Join(tmpDir, "r480_h264.mp4"))
}

func TestEncodeVideoTrackForcesSquarePixels(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "ffmpeg-args.txt")
	scriptPath := filepath.Join(tmpDir, "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$VYLUX_FFMPEG_ARGS\"\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    *.mp4)\n      mkdir -p \"$(dirname \"$arg\")\"\n      : > \"$arg\"\n      ;;\n  esac\ndone\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	ffmpegPath = scriptPath
	t.Cleanup(func() {
		ffmpegPath = oldFFmpegPath
	})
	t.Setenv("VYLUX_FFMPEG_ARGS", argsFile)

	variant := TranscodeVariant{Label: "r720_h264", Codec: CodecH264, Width: 406, Height: 720, CRF: 23}
	output := filepath.Join(tmpDir, "r720_h264.mp4")

	if err := encodeVideoTrack(context.Background(), "/tmp/input.mp4", output, variant); err != nil {
		t.Fatalf("encodeVideoTrack: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")

	assertArgsContainPair(t, args, "-vf", "scale=406:720,setsar=1")
	assertArgContains(t, args, output)
}

func assertArgContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if strings.Contains(arg, want) {
			return
		}
	}
	t.Fatalf("args do not contain %q: %v", want, args)
}

func assertArgEquals(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if arg == want {
			return
		}
	}
	t.Fatalf("args do not contain exact value %q: %v", want, args)
}

func assertArgsContainPair(t *testing.T, args []string, key, wantValue string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key {
			if args[i+1] == wantValue {
				return
			}
			t.Fatalf("arg %q has value %q, want %q", key, args[i+1], wantValue)
		}
	}
	t.Fatalf("args do not contain pair %q %q: %v", key, wantValue, args)
}

func assertArgMissing(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if arg == want {
			t.Fatalf("args unexpectedly contain %q: %v", want, args)
		}
	}
}

func assertArgDoesNotContain(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if strings.Contains(arg, want) {
			t.Fatalf("args unexpectedly contain %q in %q: %v", want, arg, args)
		}
	}
}

func assertArgOccurrences(t *testing.T, args []string, want string, count int) {
	t.Helper()
	got := 0
	for _, arg := range args {
		if arg == want {
			got++
		}
	}
	if got != count {
		t.Fatalf("args contain %q %d times, want %d: %v", want, got, count, args)
	}
}

func assertVariant(t *testing.T, variant TranscodeVariant, label string, width, height int) {
	t.Helper()
	if variant.Label != label {
		t.Fatalf("variant label = %q, want %q", variant.Label, label)
	}
	if variant.Width != width || variant.Height != height {
		t.Fatalf("variant %q dimensions = %dx%d, want %dx%d", variant.Label, variant.Width, variant.Height, width, height)
	}
}
