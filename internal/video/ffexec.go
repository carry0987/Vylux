package video

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
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

// Cmd is a thin wrapper around os/exec for ffmpeg / ffprobe commands.
type Cmd struct {
	ctx  context.Context
	bin  string
	args []string
}

// FFmpeg creates a new ffmpeg command bound to ctx.
func FFmpeg(ctx context.Context, args ...string) *Cmd {
	return &Cmd{ctx: ctx, bin: ffmpegPath, args: args}
}

// FFprobe creates a new ffprobe command bound to ctx.
func FFprobe(ctx context.Context, args ...string) *Cmd {
	return &Cmd{ctx: ctx, bin: ffprobePath, args: args}
}

// Packager creates a new Shaka Packager command bound to ctx.
func Packager(ctx context.Context, args ...string) *Cmd {
	return &Cmd{ctx: ctx, bin: packagerPath, args: args}
}

// Args appends additional arguments and returns the same Cmd for chaining.
func (c *Cmd) Args(args ...string) *Cmd {
	c.args = append(c.args, args...)

	return c
}

// Output runs the command and returns captured stdout bytes.
// On failure the error message includes stderr content.
func (c *Cmd) Output() ([]byte, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(c.ctx, c.bin, c.args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", c.bin, err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// Run executes the command without capturing stdout.
// If stderrW is non-nil, stderr is tee'd to it (useful for progress parsing).
// On failure the error message always includes stderr content.
func (c *Cmd) Run(stderrW io.Writer) error {
	var stderr bytes.Buffer

	cmd := exec.CommandContext(c.ctx, c.bin, c.args...)

	if stderrW != nil {
		cmd.Stderr = io.MultiWriter(&stderr, stderrW)
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", c.bin, err, stderr.String())
	}

	return nil
}
