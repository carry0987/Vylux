package video

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"Vylux/internal/config"
)

// TranscodeOptions configures the split-track HLS CMAF transcode.
type TranscodeOptions struct {
	Variants   []TranscodeVariant
	AudioTrack AudioTrack
	SegmentSec int // HLS segment duration (default: 6)
	Encryption *EncryptionConfig
}

// Transcode encodes split audio/video MP4 tracks with FFmpeg and then packages
// them into HLS CMAF output with Shaka Packager.
func Transcode(ctx context.Context, input string, outDir string, opts TranscodeOptions) (*TranscodeResult, error) {
	if len(opts.Variants) == 0 {
		opts.Variants = DefaultVariants()
	}
	if opts.AudioTrack.ID == "" {
		opts.AudioTrack = DefaultAudioTrack()
	}

	if opts.SegmentSec == 0 {
		opts.SegmentSec = 6
	}

	sourceWidth, sourceHeight, err := probeVideoGeometry(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("probe video geometry: %w", err)
	}
	opts.Variants = resolveTranscodeVariants(opts.Variants, sourceWidth, sourceHeight)
	slog.Debug("resolved transcode variants",
		"source_width", sourceWidth,
		"source_height", sourceHeight,
		"variants", variantLogValues(opts.Variants),
	)

	if err := os.MkdirAll(config.ScratchDir, 0o755); err != nil {
		return nil, fmt.Errorf("create scratch dir %s: %w", config.ScratchDir, err)
	}

	encodedDir, err := os.MkdirTemp(config.ScratchDir, "vylux-encoded-*")
	if err != nil {
		return nil, fmt.Errorf("create encoded dir: %w", err)
	}
	defer os.RemoveAll(encodedDir)

	hasAudio, err := HasAudio(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("probe audio: %w", err)
	}

	encodedAudioPath := ""
	if hasAudio {
		encodedAudioPath = filepath.Join(encodedDir, opts.AudioTrack.ID+".mp4")
		if err := encodeAudioTrack(ctx, input, encodedAudioPath, opts.AudioTrack); err != nil {
			return nil, err
		}
	}

	encodedVideos := make(map[string]string, len(opts.Variants))
	for _, codec := range []VideoCodec{CodecAV1, CodecH264} {
		variants := variantsForCodec(opts.Variants, codec)
		if len(variants) == 0 {
			continue
		}
		if err := encodeVideoTracks(ctx, input, encodedDir, variants); err != nil {
			return nil, err
		}
		for _, v := range variants {
			encodedVideos[v.Label] = filepath.Join(encodedDir, v.Label+".mp4")
		}
	}

	if err := packageHLS(ctx, outDir, encodedAudioPath, encodedVideos, opts); err != nil {
		return nil, err
	}

	result := &TranscodeResult{
		MasterPlaylistPath: "master.m3u8",
	}

	if hasAudio {
		segments, err := filepath.Glob(filepath.Join(outDir, filepath.FromSlash("audio/"+opts.AudioTrack.ID+"/seg_*.m4s")))
		if err != nil {
			return nil, fmt.Errorf("glob audio segments: %w", err)
		}
		result.AudioTracks = append(result.AudioTracks, PackagedAudioTrack{
			ID:           opts.AudioTrack.ID,
			Role:         opts.AudioTrack.Role,
			Language:     opts.AudioTrack.Language,
			Codec:        opts.AudioTrack.Codec,
			Channels:     opts.AudioTrack.Channels,
			Bitrate:      parseBitrate(opts.AudioTrack.Bitrate),
			PlaylistPath: audioPlaylistPath(opts.AudioTrack),
			InitPath:     audioInitPath(opts.AudioTrack),
			Segments:     segments,
		})
	}

	for _, v := range opts.Variants {
		segments, err := filepath.Glob(filepath.Join(outDir, filepath.FromSlash("video/"+v.Label+"/seg_*.m4s")))
		if err != nil {
			return nil, fmt.Errorf("glob video segments for %s: %w", v.Label, err)
		}
		track := PackagedVideoTrack{
			ID:           v.Label,
			Codec:        v.Codec,
			Width:        v.Width,
			Height:       v.Height,
			Bitrate:      estimateBandwidth(v),
			PlaylistPath: videoPlaylistPath(v),
			InitPath:     videoInitPath(v),
			Segments:     segments,
		}
		if hasAudio {
			track.AudioTrackID = opts.AudioTrack.ID
		}
		result.VideoTracks = append(result.VideoTracks, track)
	}

	return result, nil
}

func variantsForCodec(variants []TranscodeVariant, codec VideoCodec) []TranscodeVariant {
	out := make([]TranscodeVariant, 0, len(variants))
	for _, v := range variants {
		if v.Codec == codec {
			out = append(out, v)
		}
	}

	return out
}

func variantLogValues(variants []TranscodeVariant) []string {
	values := make([]string, 0, len(variants))
	for _, variant := range variants {
		values = append(values, fmt.Sprintf("%s=%dx%d/%s", variant.Label, variant.Width, variant.Height, variant.Codec))
	}

	return values
}

func encodeVideoTracks(ctx context.Context, input, outDir string, variants []TranscodeVariant) error {
	if len(variants) == 0 {
		return nil
	}
	if len(variants) == 1 {
		return encodeVideoTrack(ctx, input, filepath.Join(outDir, variants[0].Label+".mp4"), variants[0])
	}

	for _, variant := range variants {
		outputPath := filepath.Join(outDir, variant.Label+".mp4")
		if err := ensureDir(filepath.Dir(outputPath)); err != nil {
			return fmt.Errorf("create video dir %s: %w", filepath.Dir(outputPath), err)
		}
	}

	args := []string{"-y", "-i", input, "-filter_complex", buildSharedVideoFilter(variants)}
	for i, variant := range variants {
		args = append(args, "-map", fmt.Sprintf("[vout%d]", i), "-an")
		switch variant.Codec {
		case CodecAV1:
			args = append(args, "-c:v", "libsvtav1", "-preset", "6", "-crf", fmt.Sprintf("%d", variant.CRF))
		default:
			args = append(args, "-c:v", "libx264", "-preset", "fast", "-crf", fmt.Sprintf("%d", variant.CRF))
		}
		args = append(args,
			"-movflags", "+faststart",
			filepath.Join(outDir, variant.Label+".mp4"),
		)
	}

	slog.Debug("ffmpeg encode video tracks", "codec", variants[0].Codec, "track_count", len(variants), "args", strings.Join(args, " "))
	if err := FFmpeg(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("encode video tracks (%s): %w", variants[0].Codec, err)
	}

	return nil
}

func buildSharedVideoFilter(variants []TranscodeVariant) string {
	labels := make([]string, 0, len(variants))
	parts := make([]string, 0, len(variants)+1)
	for i := range variants {
		labels = append(labels, fmt.Sprintf("[vsrc%d]", i))
	}
	parts = append(parts, fmt.Sprintf("[0:v:0]split=%d%s", len(variants), strings.Join(labels, "")))
	for i, variant := range variants {
		parts = append(parts, fmt.Sprintf("[vsrc%d]scale=%d:%d,setsar=1[vout%d]", i, variant.Width, variant.Height, i))
	}

	return strings.Join(parts, ";")
}

func encodeVideoTrack(ctx context.Context, input, output string, variant TranscodeVariant) error {
	if err := ensureDir(filepath.Dir(output)); err != nil {
		return fmt.Errorf("create video dir %s: %w", filepath.Dir(output), err)
	}

	args := []string{"-y", "-i", input, "-map", "0:v:0", "-an"}
	switch variant.Codec {
	case CodecAV1:
		args = append(args, "-c:v", "libsvtav1", "-preset", "6", "-crf", fmt.Sprintf("%d", variant.CRF))
	default:
		args = append(args, "-c:v", "libx264", "-preset", "fast", "-crf", fmt.Sprintf("%d", variant.CRF))
	}
	args = append(args,
		"-vf", fmt.Sprintf("scale=%d:%d,setsar=1", variant.Width, variant.Height),
		"-movflags", "+faststart",
		output,
	)

	slog.Debug("ffmpeg encode video track", "track", variant.Label, "args", strings.Join(args, " "))
	if err := FFmpeg(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("encode video track %s: %w", variant.Label, err)
	}

	return nil
}

func encodeAudioTrack(ctx context.Context, input, output string, track AudioTrack) error {
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

	slog.Debug("ffmpeg encode audio track", "track", track.ID, "args", strings.Join(args, " "))
	if err := FFmpeg(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("encode audio track %s: %w", track.ID, err)
	}

	return nil
}

func packageHLS(ctx context.Context, outDir, audioPath string, encodedVideos map[string]string, opts TranscodeOptions) error {
	if err := ensureDir(outDir); err != nil {
		return fmt.Errorf("create output dir %s: %w", outDir, err)
	}

	args := make([]string, 0, len(opts.Variants)+8)
	if audioPath != "" {
		args = append(args, buildAudioDescriptor(filepath.Clean(audioPath), outDir, opts.AudioTrack))
	}
	for _, v := range opts.Variants {
		videoPath, ok := encodedVideos[v.Label]
		if !ok {
			return fmt.Errorf("missing encoded video track %s", v.Label)
		}
		args = append(args, buildVideoDescriptor(filepath.Clean(videoPath), outDir, v))
	}

	args = append(args,
		"--hls_master_playlist_output", filepath.Join(outDir, "master.m3u8"),
		"--hls_playlist_type", "VOD",
		"--segment_duration", fmt.Sprintf("%d", opts.SegmentSec),
		"--fragment_duration", fmt.Sprintf("%d", opts.SegmentSec),
	)
	if audioPath != "" && hasPackagerLanguage(opts.AudioTrack.Language) {
		args = append(args, "--default_language", opts.AudioTrack.Language)
	}
	if opts.Encryption != nil {
		if len(opts.Encryption.Key) == 0 || opts.Encryption.KeyID == "" || opts.Encryption.HLSKeyURI == "" {
			return errors.New("incomplete encryption config")
		}
		scheme := opts.Encryption.ProtectionScheme
		if scheme == "" {
			scheme = "cbcs"
		}
		args = append(args,
			"--enable_raw_key_encryption",
			"--protection_scheme", scheme,
			"--clear_lead", "0",
			"--keys", "label=:key_id="+opts.Encryption.KeyID+":key="+keyHex(opts.Encryption.Key),
			"--hls_key_uri", opts.Encryption.HLSKeyURI,
		)
	}

	slog.Debug("shaka package hls", "args", strings.Join(args, " "))
	if err := Packager(ctx, args...).Run(os.Stderr); err != nil {
		return fmt.Errorf("package hls: %w", err)
	}

	return nil
}

func buildAudioDescriptor(input, outDir string, track AudioTrack) string {
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

func buildVideoDescriptor(input, outDir string, variant TranscodeVariant) string {
	fields := []string{
		"in=" + input,
		"stream=video",
		"init_segment=" + filepath.Join(outDir, filepath.FromSlash(videoInitPath(variant))),
		"segment_template=" + filepath.Join(outDir, filepath.FromSlash(videoSegmentPattern(variant))),
		"playlist_name=" + videoPlaylistPath(variant),
		"bw=" + formatBandwidth(variant),
	}

	return strings.Join(fields, ",")
}

// estimateBandwidth returns a rough bandwidth estimate for the HLS manifest.
// AV1 streams use ~40 % less bandwidth than H.264 at equivalent quality.
func estimateBandwidth(v TranscodeVariant) int {
	var base int
	switch {
	case v.Height >= 1080:
		base = 5_000_000
	case v.Height >= 720:
		base = 2_500_000
	case v.Height >= 480:
		base = 1_000_000
	case v.Height >= 360:
		base = 500_000
	case v.Height >= 240:
		base = 300_000
	default:
		base = 200_000
	}

	if v.Codec == CodecAV1 {
		return base * 60 / 100 // AV1 ~40 % savings
	}

	return base
}
