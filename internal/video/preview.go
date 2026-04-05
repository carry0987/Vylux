package video

import (
	"context"
	"fmt"
)

// PreviewOptions configures the animated preview output.
type PreviewOptions struct {
	StartSec         float64 // seek position (default: 2)
	Duration         float64 // clip length in seconds (default: 3)
	Width            int     // output width (default: 480, height auto)
	FPS              int     // frame rate (default: 10)
	Format           string  // "webp" (default) or "gif"
	Quality          int     // q:v for WebP (default: 70, ignored for GIF)
	CompressionLevel int     // compression_level for WebP 0-6 (default: 6, ignored for GIF)
}

// DefaultPreviewOptions returns sensible defaults matching the design doc.
func DefaultPreviewOptions() PreviewOptions {
	return PreviewOptions{
		StartSec:         2,
		Duration:         3,
		Width:            480,
		FPS:              10,
		Format:           "webp",
		Quality:          70,
		CompressionLevel: 6,
	}
}

// GeneratePreview creates an animated preview clip from the video.
func GeneratePreview(ctx context.Context, input string, opts PreviewOptions) (*PreviewResult, error) {
	if opts.Width == 0 {
		opts.Width = 480
	}

	if opts.FPS == 0 {
		opts.FPS = 10
	}

	if opts.Format == "" {
		opts.Format = "webp"
	}

	if opts.Duration == 0 {
		opts.Duration = 3
	}

	if opts.Quality == 0 {
		opts.Quality = 70
	}

	if opts.CompressionLevel == 0 {
		opts.CompressionLevel = 6
	}

	vf := fmt.Sprintf("scale=%d:-1,fps=%d", opts.Width, opts.FPS)

	// Build format-specific FFmpeg arguments.
	args := []string{
		"-ss", fmt.Sprintf("%.2f", opts.StartSec),
		"-i", input,
		"-t", fmt.Sprintf("%.2f", opts.Duration),
		"-vf", vf,
		"-loop", "0",
		"-an",
	}

	switch opts.Format {
	case "gif":
		args = append(args, "-f", "gif")
	default: // webp
		args = append(args,
			"-vcodec", "libwebp",
			"-q:v", fmt.Sprintf("%d", opts.Quality),
			"-compression_level", fmt.Sprintf("%d", opts.CompressionLevel),
			"-f", "webp",
		)
	}

	args = append(args, "pipe:1")

	data, err := FFmpeg(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("generate preview: %w", err)
	}

	return &PreviewResult{
		Data:   data,
		Format: opts.Format,
	}, nil
}
