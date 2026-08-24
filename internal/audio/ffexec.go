package audio

import (
	"context"

	"Vylux/internal/mediaexec"
)

var (
	ffmpegPath   = "ffmpeg"
	packagerPath = "packager"
)

// SetFFmpegPath overrides the default ffmpeg binary location.
func SetFFmpegPath(path string) {
	if path != "" {
		ffmpegPath = path
	}
}

// SetPackagerPath overrides the default Shaka Packager binary location.
func SetPackagerPath(path string) {
	if path != "" {
		packagerPath = path
	}
}

func ffmpeg(ctx context.Context, args ...string) *mediaexec.Cmd {
	return mediaexec.New(ctx, ffmpegPath, args...)
}

func packager(ctx context.Context, args ...string) *mediaexec.Cmd {
	return mediaexec.New(ctx, packagerPath, args...)
}
