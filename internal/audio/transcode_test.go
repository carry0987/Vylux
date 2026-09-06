package audio

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"Vylux/internal/config"
)

func TestEncodeMP3BuildsExpectedArgs(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "ffmpeg-args.txt")
	scriptPath := filepath.Join(tmpDir, "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"$VYLUX_FFMPEG_ARGS\"\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    *.mp3)\n      mkdir -p \"$(dirname \"$arg\")\"\n      : > \"$arg\"\n      ;;\n  esac\ndone\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	ffmpegPath = scriptPath
	t.Cleanup(func() { ffmpegPath = oldFFmpegPath })
	t.Setenv("VYLUX_FFMPEG_ARGS", argsFile)

	out := filepath.Join(tmpDir, "downloads", "track.mp3")
	if err := EncodeMP3(context.Background(), "/tmp/input.flac", out, "192k"); err != nil {
		t.Fatalf("EncodeMP3: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	assertArgsContainPair(t, args, "-map", "0:a:0")
	assertArgsContainPair(t, args, "-c:a", "libmp3lame")
	assertArgsContainPair(t, args, "-b:a", "192k")
	assertArgEquals(t, args, out)
}

func TestEncodeFLACBuildsExpectedArgs(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "ffmpeg-args.txt")
	scriptPath := filepath.Join(tmpDir, "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"$VYLUX_FFMPEG_ARGS\"\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    *.flac)\n      mkdir -p \"$(dirname \"$arg\")\"\n      : > \"$arg\"\n      ;;\n  esac\ndone\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	ffmpegPath = scriptPath
	t.Cleanup(func() { ffmpegPath = oldFFmpegPath })
	t.Setenv("VYLUX_FFMPEG_ARGS", argsFile)

	out := filepath.Join(tmpDir, "downloads", "track.flac")
	if err := EncodeFLAC(context.Background(), "/tmp/input.wav", out); err != nil {
		t.Fatalf("EncodeFLAC: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	assertArgsContainPair(t, args, "-map", "0:a:0")
	assertArgsContainPair(t, args, "-c:a", "flac")
	assertArgEquals(t, args, out)
}

func TestPackageHLSBuildsExpectedArgs(t *testing.T) {
	tmpDir := t.TempDir()
	packagerArgsFile := filepath.Join(tmpDir, "packager-args.txt")
	ffmpegScriptPath := filepath.Join(tmpDir, "ffmpeg")
	ffmpegScript := "#!/bin/sh\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    *.mp4)\n      mkdir -p \"$(dirname \"$arg\")\"\n      : > \"$arg\"\n      ;;\n  esac\ndone\n"
	if err := os.WriteFile(ffmpegScriptPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}
	packagerScriptPath := filepath.Join(tmpDir, "packager")
	packagerScript := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"$VYLUX_PACKAGER_ARGS\"\nmkdir -p \"$VYLUX_HLS_DIR/hls/aac_128\"\n: > \"$VYLUX_HLS_DIR/hls/master.m3u8\"\n: > \"$VYLUX_HLS_DIR/hls/aac_128/init.mp4\"\n: > \"$VYLUX_HLS_DIR/hls/aac_128/playlist.m3u8\"\n: > \"$VYLUX_HLS_DIR/hls/aac_128/seg_1.m4s\"\n"
	if err := os.WriteFile(packagerScriptPath, []byte(packagerScript), 0o755); err != nil {
		t.Fatalf("write packager stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	oldPackagerPath := packagerPath
	oldScratchDir := config.ScratchDir
	ffmpegPath = ffmpegScriptPath
	packagerPath = packagerScriptPath
	config.ScratchDir = tmpDir
	t.Cleanup(func() {
		ffmpegPath = oldFFmpegPath
		packagerPath = oldPackagerPath
		config.ScratchDir = oldScratchDir
	})
	t.Setenv("VYLUX_PACKAGER_ARGS", packagerArgsFile)
	outDir := filepath.Join(tmpDir, "out")
	t.Setenv("VYLUX_HLS_DIR", outDir)

	result, err := PackageHLS(context.Background(), "/tmp/input.flac", outDir, &HLSOptions{
		Track:      HLSTrack{ID: "aac_128", Role: "main", Language: "en", Codec: "aac", Channels: 2, Bitrate: "128k"},
		SegmentSec: 4,
	})
	if err != nil {
		t.Fatalf("PackageHLS: %v", err)
	}

	data, err := os.ReadFile(packagerArgsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	assertArgContains(t, args, "stream=audio")
	assertArgContains(t, args, "playlist_name="+filepath.Join(outDir, filepath.FromSlash("hls/aac_128/playlist.m3u8")))
	assertArgsContainPair(t, args, "--hls_playlist_type", "VOD")
	assertArgsContainPair(t, args, "--segment_duration", "4")
	assertArgsContainPair(t, args, "--fragment_duration", "4")
	assertArgsContainPair(t, args, "--default_language", "en")

	if result.MasterPlaylistPath != "hls/master.m3u8" {
		t.Fatalf("master playlist = %q", result.MasterPlaylistPath)
	}
	if len(result.Renditions) != 1 {
		t.Fatalf("rendition count = %d, want 1", len(result.Renditions))
	}
	if result.Renditions[0].PlaylistPath != "hls/aac_128/playlist.m3u8" {
		t.Fatalf("playlist path = %q", result.Renditions[0].PlaylistPath)
	}
	if len(result.Renditions[0].Segments) != 1 {
		t.Fatalf("segment count = %d, want 1", len(result.Renditions[0].Segments))
	}
}

func TestPackageHLSIncludesEncryptionArgs(t *testing.T) {
	tmpDir := t.TempDir()
	packagerArgsFile := filepath.Join(tmpDir, "packager-args.txt")
	ffmpegScriptPath := filepath.Join(tmpDir, "ffmpeg")
	ffmpegScript := "#!/bin/sh\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    *.mp4)\n      mkdir -p \"$(dirname \"$arg\")\"\n      : > \"$arg\"\n      ;;\n  esac\ndone\n"
	if err := os.WriteFile(ffmpegScriptPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatalf("write ffmpeg stub: %v", err)
	}
	packagerScriptPath := filepath.Join(tmpDir, "packager")
	packagerScript := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"$VYLUX_PACKAGER_ARGS\"\nmkdir -p \"$VYLUX_HLS_DIR/hls/aac_128\"\n: > \"$VYLUX_HLS_DIR/hls/master.m3u8\"\n: > \"$VYLUX_HLS_DIR/hls/aac_128/init.mp4\"\n: > \"$VYLUX_HLS_DIR/hls/aac_128/playlist.m3u8\"\n: > \"$VYLUX_HLS_DIR/hls/aac_128/seg_1.m4s\"\n"
	if err := os.WriteFile(packagerScriptPath, []byte(packagerScript), 0o755); err != nil {
		t.Fatalf("write packager stub: %v", err)
	}

	oldFFmpegPath := ffmpegPath
	oldPackagerPath := packagerPath
	oldScratchDir := config.ScratchDir
	ffmpegPath = ffmpegScriptPath
	packagerPath = packagerScriptPath
	config.ScratchDir = tmpDir
	t.Cleanup(func() {
		ffmpegPath = oldFFmpegPath
		packagerPath = oldPackagerPath
		config.ScratchDir = oldScratchDir
	})
	t.Setenv("VYLUX_PACKAGER_ARGS", packagerArgsFile)
	outDir := filepath.Join(tmpDir, "out")
	t.Setenv("VYLUX_HLS_DIR", outDir)

	keyID := "00112233445566778899aabbccddeeff"
	key := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}
	_, err := PackageHLS(context.Background(), "/tmp/input.flac", outDir, &HLSOptions{
		Track:      HLSTrack{ID: "aac_128", Role: "main", Language: "en", Codec: "aac", Channels: 2, Bitrate: "128k"},
		SegmentSec: 4,
		Encryption: &EncryptionConfig{
			KeyID:     keyID,
			Key:       key,
			HLSKeyURI: "https://media.example.com/api/key/audio_hash123",
		},
	})
	if err != nil {
		t.Fatalf("PackageHLS: %v", err)
	}

	data, err := os.ReadFile(packagerArgsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	assertArgEquals(t, args, "--enable_raw_key_encryption")
	assertArgsContainPair(t, args, "--protection_scheme", "cbcs")
	assertArgsContainPair(t, args, "--keys", "label=:key_id="+keyID+":key="+keyHex(key))
	assertArgsContainPair(t, args, "--hls_key_uri", "https://media.example.com/api/key/audio_hash123")
}

func TestBuildAudioDescriptorUsesAbsolutePlaylistPath(t *testing.T) {
	outDir := t.TempDir()
	track := &HLSTrack{ID: "aac_128", Role: "main", Language: "en", Codec: "aac", Channels: 2, Bitrate: "128k"}

	descriptor := buildAudioDescriptor("/tmp/input.mp4", outDir, track)

	assertArgContains(t, []string{descriptor}, "playlist_name="+filepath.Join(outDir, filepath.FromSlash(audioPlaylistPath(track))))
	assertArgContains(t, []string{descriptor}, "init_segment="+filepath.Join(outDir, filepath.FromSlash(audioInitPath(track))))
	assertArgContains(t, []string{descriptor}, "segment_template="+filepath.Join(outDir, filepath.FromSlash(audioSegmentPattern(track))))
}

func assertArgContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if strings.Contains(arg, want) {
			return
		}
	}
	t.Fatalf("args %v do not contain %q", args, want)
}

func assertArgEquals(t *testing.T, args []string, want string) {
	t.Helper()
	if slices.Contains(args, want) {
		return
	}
	t.Fatalf("args %v do not contain exact %q", args, want)
}

func assertArgsContainPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Fatalf("args %v do not contain pair %q %q", args, key, value)
}
