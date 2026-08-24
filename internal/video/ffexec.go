package video

import (
	"context"

	"Vylux/internal/mediaexec"
)

// Binary paths — default to PATH lookup.
var (
	ffmpegPath   = "ffmpeg"
	ffprobePath  = "ffprobe"
	packagerPath = "packager"
)

// SetFFmpegPath overrides the default ffmpeg binary location.
func SetFFmpegPath(path string) {
	if path != "" {
		ffmpegPath = path
	}
}

// SetFFprobePath overrides the default ffprobe binary location.
func SetFFprobePath(path string) {
	if path != "" {
		ffprobePath = path
	}
}

// SetPackagerPath overrides the default Shaka Packager binary location.
func SetPackagerPath(path string) {
	if path != "" {
		packagerPath = path
	}
}

// Cmd is a thin wrapper around mediaexec for ffmpeg / ffprobe commands.
type Cmd = mediaexec.Cmd

// FFmpeg creates a new ffmpeg command bound to ctx.
func FFmpeg(ctx context.Context, args ...string) *Cmd {
	return mediaexec.New(ctx, ffmpegPath, args...)
}

// FFprobe creates a new ffprobe command bound to ctx.
func FFprobe(ctx context.Context, args ...string) *Cmd {
	return mediaexec.New(ctx, ffprobePath, args...)
}

// Packager creates a new Shaka Packager command bound to ctx.
func Packager(ctx context.Context, args ...string) *Cmd {
	return mediaexec.New(ctx, packagerPath, args...)
}
