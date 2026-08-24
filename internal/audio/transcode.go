package audio

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"Vylux/internal/config"
)

// Rendition describes a packaged audio rendition.
type Rendition struct {
	ID           string
	Role         string
	Language     string
	Codec        string
	Channels     int
	Bitrate      int
	PlaylistPath string
	InitPath     string
	Segments     []string
}

// HLSResult describes audio-only HLS output.
type HLSResult struct {
	MasterPlaylistPath string
	Renditions         []Rendition
}

// HLSTrack configures the encoded audio track used for audio-only HLS.
type HLSTrack struct {
	ID       string
	Role     string
	Language string
	Codec    string
	Channels int
	Bitrate  string
}

// HLSOptions configures audio-only HLS packaging.
type HLSOptions struct {
	Track      HLSTrack
	SegmentSec int
}

// DefaultHLSTrack returns the baseline audio rendition for playback.
func DefaultHLSTrack() HLSTrack {
	return HLSTrack{
		ID:       "aac_128",
		Role:     "main",
		Language: "und",
		Codec:    "aac",
		Channels: 2,
		Bitrate:  "128k",
	}
}

// PackageHLS creates an audio-only HLS package from the input source.
func PackageHLS(ctx context.Context, input, outDir string, opts *HLSOptions) (*HLSResult, error) {
	if opts == nil {
		opts = &HLSOptions{}
	}
	if opts.Track.ID == "" {
		opts.Track = DefaultHLSTrack()
	}
	if opts.SegmentSec == 0 {
		opts.SegmentSec = 6
	}

	if err := ensureDir(outDir); err != nil {
		return nil, fmt.Errorf("create output dir %s: %w", outDir, err)
	}
	if err := os.MkdirAll(config.ScratchDir, 0o755); err != nil {
		return nil, fmt.Errorf("create scratch dir %s: %w", config.ScratchDir, err)
	}

	encodedDir, err := os.MkdirTemp(config.ScratchDir, "vylux-audio-encoded-*")
	if err != nil {
		return nil, fmt.Errorf("create encoded dir: %w", err)
	}
	defer os.RemoveAll(encodedDir)

	encodedTrack := filepath.Join(encodedDir, opts.Track.ID+".mp4")
	if err := encodeHLSTrack(ctx, input, encodedTrack, &opts.Track); err != nil {
		return nil, err
	}
	if err := packageAudioHLS(ctx, outDir, encodedTrack, opts); err != nil {
		return nil, err
	}

	segments, err := filepath.Glob(filepath.Join(outDir, filepath.FromSlash(audioSegmentGlob(&opts.Track))))
	if err != nil {
		return nil, fmt.Errorf("glob audio segments: %w", err)
	}

	return &HLSResult{
		MasterPlaylistPath: filepathJoin("hls", "master.m3u8"),
		Renditions: []Rendition{{
			ID:           opts.Track.ID,
			Role:         opts.Track.Role,
			Language:     opts.Track.Language,
			Codec:        opts.Track.Codec,
			Channels:     opts.Track.Channels,
			Bitrate:      parseBitrate(opts.Track.Bitrate),
			PlaylistPath: audioPlaylistPath(&opts.Track),
			InitPath:     audioInitPath(&opts.Track),
			Segments:     segments,
		}},
	}, nil
}

// EncodeMP3 transcodes the input source to an MP3 file.
func EncodeMP3(ctx context.Context, input, output, bitrate string) error {
	if bitrate == "" {
		bitrate = "320k"
	}
	if err := ensureDir(filepath.Dir(output)); err != nil {
		return fmt.Errorf("create output dir %s: %w", filepath.Dir(output), err)
	}

	args := []string{
		"-y", "-i", input,
		"-map", "0:a:0",
		"-vn",
		"-c:a", "libmp3lame",
		"-b:a", bitrate,
		output,
	}

	slog.Debug("ffmpeg encode mp3", "output", output, "args", strings.Join(args, " "))
	if err := ffmpeg(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("encode mp3: %w", err)
	}

	return nil
}

// EncodeFLAC transcodes the input source to a FLAC file.
func EncodeFLAC(ctx context.Context, input, output string) error {
	if err := ensureDir(filepath.Dir(output)); err != nil {
		return fmt.Errorf("create output dir %s: %w", filepath.Dir(output), err)
	}

	args := []string{
		"-y", "-i", input,
		"-map", "0:a:0",
		"-vn",
		"-c:a", "flac",
		output,
	}

	slog.Debug("ffmpeg encode flac", "output", output, "args", strings.Join(args, " "))
	if err := ffmpeg(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("encode flac: %w", err)
	}

	return nil
}

func encodeHLSTrack(ctx context.Context, input, output string, track *HLSTrack) error {
	if err := ensureDir(filepath.Dir(output)); err != nil {
		return fmt.Errorf("create audio dir %s: %w", filepath.Dir(output), err)
	}

	args := []string{
		"-y", "-i", input,
		"-map", "0:a:0",
		"-vn",
		"-c:a", "aac",
		"-b:a", track.Bitrate,
		"-movflags", "+faststart",
		output,
	}

	slog.Debug("ffmpeg encode audio hls track", "track", track.ID, "args", strings.Join(args, " "))
	if err := ffmpeg(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("encode audio track %s: %w", track.ID, err)
	}

	return nil
}

func packageAudioHLS(ctx context.Context, outDir, encodedTrack string, opts *HLSOptions) error {
	args := []string{
		buildAudioDescriptor(filepath.Clean(encodedTrack), outDir, &opts.Track),
		"--hls_master_playlist_output", filepath.Join(outDir, filepath.FromSlash(filepathJoin("hls", "master.m3u8"))),
		"--hls_playlist_type", "VOD",
		"--segment_duration", strconv.Itoa(opts.SegmentSec),
		"--fragment_duration", strconv.Itoa(opts.SegmentSec),
	}
	if hasPackagerLanguage(opts.Track.Language) {
		args = append(args, "--default_language", opts.Track.Language)
	}

	slog.Debug("shaka package audio hls", "args", strings.Join(args, " "))
	if err := packager(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("package audio hls: %w", err)
	}

	return nil
}

func buildAudioDescriptor(input, outDir string, track *HLSTrack) string {
	fields := []string{
		"in=" + input,
		"stream=audio",
		"init_segment=" + filepath.Join(outDir, filepath.FromSlash(audioInitPath(track))),
		"segment_template=" + filepath.Join(outDir, filepath.FromSlash(audioSegmentPattern(track))),
		"playlist_name=" + audioPlaylistPath(track),
		"hls_group_id=audio",
		"hls_name=" + audioName(track),
	}
	if hasPackagerLanguage(track.Language) {
		fields = append(fields, "lang="+track.Language)
	}

	return strings.Join(fields, ",")
}

func hasPackagerLanguage(language string) bool {
	language = strings.TrimSpace(strings.ToLower(language))
	return language != "" && language != "und"
}

func audioName(track *HLSTrack) string {
	if track.Role != "" {
		return strings.ToUpper(track.Role[:1]) + track.Role[1:]
	}
	return "Audio"
}

func audioInitPath(track *HLSTrack) string {
	return filepathJoin("hls", track.ID, "init.mp4")
}

func audioPlaylistPath(track *HLSTrack) string {
	return filepathJoin("hls", track.ID, "playlist.m3u8")
}

func audioSegmentPattern(track *HLSTrack) string {
	return filepathJoin("hls", track.ID, "seg_$Number$.m4s")
}

func audioSegmentGlob(track *HLSTrack) string {
	return filepathJoin("hls", track.ID, "seg_*.m4s")
}

func filepathJoin(parts ...string) string {
	return strings.Join(parts, "/")
}

func parseBitrate(value string) int {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" {
		return 0
	}
	multiplier := 1
	if strings.HasSuffix(v, "k") {
		multiplier = 1000
		v = strings.TrimSuffix(v, "k")
	} else if strings.HasSuffix(v, "m") {
		multiplier = 1000 * 1000
		v = strings.TrimSuffix(v, "m")
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}

	return n * multiplier
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
