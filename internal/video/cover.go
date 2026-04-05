package video

import (
	"context"
	"fmt"
)

// ExtractCover extracts a single frame from the video at the given timestamp
// (in seconds) and returns the raw JPEG bytes.
// The caller can pipe the output into vipsgen for further processing.
func ExtractCover(ctx context.Context, input string, timestampSec float64) (*CoverResult, error) {
	ss := fmt.Sprintf("%.2f", timestampSec)

	data, err := FFmpeg(ctx,
		"-ss", ss,
		"-i", input,
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", "2",
		"pipe:1",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("extract cover at %ss: %w", ss, err)
	}

	return &CoverResult{
		Data:   data,
		Format: "jpg",
	}, nil
}
