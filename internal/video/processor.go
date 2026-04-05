package video

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// CoverResult holds the output of a cover extraction.
type CoverResult struct {
	Data   []byte
	Format string // "jpg"
}

// PreviewResult holds the output of a preview generation.
type PreviewResult struct {
	Data   []byte
	Format string // "webp" or "gif"
}

// VideoCodec identifies the video codec for transcoding.
type VideoCodec string

const (
	CodecH264 VideoCodec = "h264" // libx264 — maximum compatibility
	CodecAV1  VideoCodec = "av1"  // libsvtav1 (SVT-AV1) — best compression
)

// TranscodeVariant describes one HLS output variant.
type TranscodeVariant struct {
	Label  string     // e.g. "r1080_av1", "r720_h264"
	Codec  VideoCodec // video codec (default: CodecH264)
	Width  int
	Height int
	CRF    int
	ABitR  string // audio bitrate, e.g. "128k"
}

type canonicalVariantSpec struct {
	Rung      int
	Width     int
	Height    int
	AV1CRF    int
	H264CRF   int
	AudioRate string
}

func canonicalVariantSpecs() []canonicalVariantSpec {
	return []canonicalVariantSpec{
		{Rung: 1080, Width: 1920, Height: 1080, AV1CRF: 30, H264CRF: 22, AudioRate: "128k"},
		{Rung: 720, Width: 1280, Height: 720, AV1CRF: 32, H264CRF: 23, AudioRate: "96k"},
		{Rung: 480, Width: 854, Height: 480, AV1CRF: 34, H264CRF: 24, AudioRate: "64k"},
		{Rung: 360, Width: 640, Height: 360, AV1CRF: 36, H264CRF: 26, AudioRate: "48k"},
		{Rung: 240, Width: 426, Height: 240, AV1CRF: 38, H264CRF: 28, AudioRate: "32k"},
	}
}

func rungLabel(rung int, codec VideoCodec) string {
	return fmt.Sprintf("r%d_%s", rung, codec)
}

// AudioTrack describes the shared audio rendition used by all video variants.
type AudioTrack struct {
	ID       string
	Role     string
	Language string
	Codec    string
	Channels int
	Bitrate  string
}

// DefaultVariants returns AV1 + H.264 dual-track variants.
// Players that support AV1 will pick the AV1 streams (better compression);
// older clients fall back to the H.264 streams automatically.
func DefaultVariants() []TranscodeVariant {
	variants := make([]TranscodeVariant, 0, len(canonicalVariantSpecs())*2)
	for _, spec := range canonicalVariantSpecs() {
		variants = append(variants,
			TranscodeVariant{Label: rungLabel(spec.Rung, CodecAV1), Codec: CodecAV1, Width: spec.Width, Height: spec.Height, CRF: spec.AV1CRF, ABitR: spec.AudioRate},
			TranscodeVariant{Label: rungLabel(spec.Rung, CodecH264), Codec: CodecH264, Width: spec.Width, Height: spec.Height, CRF: spec.H264CRF, ABitR: spec.AudioRate},
		)
	}

	return variants
}

// H264OnlyVariants returns H.264-only variants for environments without
// SVT-AV1 support or when encoding speed is critical.
func H264OnlyVariants() []TranscodeVariant {
	variants := make([]TranscodeVariant, 0, len(canonicalVariantSpecs()))
	for _, spec := range canonicalVariantSpecs() {
		variants = append(variants, TranscodeVariant{
			Label:  rungLabel(spec.Rung, CodecH264),
			Codec:  CodecH264,
			Width:  spec.Width,
			Height: spec.Height,
			CRF:    spec.H264CRF,
			ABitR:  spec.AudioRate,
		})
	}

	return variants
}

// DefaultAudioTrack returns the default audio rendition used for HLS CMAF packaging.
func DefaultAudioTrack() AudioTrack {
	return AudioTrack{
		ID:       "und_aac_2ch",
		Role:     "main",
		Language: "und",
		Codec:    "aac",
		Channels: 2,
		Bitrate:  "128k",
	}
}

// codecsString returns the CODECS value for the HLS master playlist
// (RFC 6381) so players can select the correct variant without downloading segments.
func codecsString(v TranscodeVariant) string {
	var vc string
	switch v.Codec {
	case CodecAV1:
		vc = "av01.0.08M.08" // Main profile, Level 4.0, Main tier, 8-bit
	default:
		vc = "avc1.640028" // High profile, Level 4.0
	}

	return vc + ",mp4a.40.2" // + AAC-LC audio
}

// TranscodeResult describes a single variant output after transcoding.
type TranscodeResult struct {
	MasterPlaylistPath string
	AudioTracks        []PackagedAudioTrack
	VideoTracks        []PackagedVideoTrack
}

// PackagedAudioTrack describes one packaged audio HLS track.
type PackagedAudioTrack struct {
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

// PackagedVideoTrack describes one packaged video HLS track.
type PackagedVideoTrack struct {
	ID           string
	Codec        VideoCodec
	Width        int
	Height       int
	Bitrate      int
	PlaylistPath string
	InitPath     string
	Segments     []string
	AudioTrackID string
}

// EncryptionConfig carries the metadata needed by Shaka Packager raw-key encryption.
type EncryptionConfig struct {
	KeyID            string
	Key              []byte
	ProtectionScheme string
	HLSKeyURI        string
}

// Probe returns ffprobe JSON output for a media file.
func Probe(input string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := FFprobe(ctx,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		input,
	).Output()
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// HasAudio reports whether the input contains at least one audio stream.
func HasAudio(ctx context.Context, input string) (bool, error) {
	out, err := FFprobe(ctx,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		input,
	).Output()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(string(out)) != "", nil
}

type probeStreamsOutput struct {
	Streams []probeVideoStream `json:"streams"`
}

type probeVideoStream struct {
	Width        int                    `json:"width"`
	Height       int                    `json:"height"`
	Tags         map[string]string      `json:"tags"`
	SideDataList []probeVideoStreamSide `json:"side_data_list"`
}

type probeVideoStreamSide struct {
	Rotation float64 `json:"rotation"`
}

func probeVideoGeometry(ctx context.Context, input string) (int, int, error) {
	out, err := FFprobe(ctx,
		"-v", "quiet",
		"-print_format", "json",
		"-select_streams", "v:0",
		"-show_streams",
		input,
	).Output()
	if err != nil {
		return 0, 0, err
	}

	var probe probeStreamsOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return 0, 0, fmt.Errorf("parse ffprobe video geometry: %w", err)
	}
	if len(probe.Streams) == 0 {
		return 0, 0, fmt.Errorf("no video stream found")
	}

	stream := probe.Streams[0]
	width, height := stream.Width, stream.Height
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("invalid video dimensions %dx%d", width, height)
	}
	if rotation := stream.rotation(); rotation == 90 || rotation == 270 {
		width, height = height, width
	}

	return width, height, nil
}

func (s probeVideoStream) rotation() int {
	for _, sideData := range s.SideDataList {
		if sideData.Rotation != 0 {
			return normalizeRotation(sideData.Rotation)
		}
	}
	if raw, ok := s.Tags["rotate"]; ok {
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err == nil {
			return normalizeRotation(value)
		}
	}

	return 0
}

func normalizeRotation(value float64) int {
	rotation := int(math.Round(value)) % 360
	if rotation < 0 {
		rotation += 360
	}
	if rotation%90 != 0 {
		return 0
	}

	return rotation
}

func resolveTranscodeVariants(templates []TranscodeVariant, sourceWidth, sourceHeight int) []TranscodeVariant {
	if len(templates) == 0 || sourceWidth <= 0 || sourceHeight <= 0 {
		return templates
	}

	shortEdge := sourceHeight
	if sourceWidth < shortEdge {
		shortEdge = sourceWidth
	}

	groups := make(map[VideoCodec][]TranscodeVariant)
	codecOrder := make([]VideoCodec, 0, len(templates))
	for _, template := range templates {
		if _, ok := groups[template.Codec]; !ok {
			codecOrder = append(codecOrder, template.Codec)
		}
		groups[template.Codec] = append(groups[template.Codec], template)
	}

	resolved := make([]TranscodeVariant, 0, len(templates))
	for _, codec := range codecOrder {
		resolved = append(resolved, selectVariantsForCodec(groups[codec], sourceWidth, sourceHeight, shortEdge)...)
	}

	return resolved
}

func selectVariantsForCodec(templates []TranscodeVariant, sourceWidth, sourceHeight, shortEdge int) []TranscodeVariant {
	selected := make([]TranscodeVariant, 0, len(templates))
	for _, template := range templates {
		targetShortEdge := template.Height
		if template.Width < targetShortEdge {
			targetShortEdge = template.Width
		}
		if targetShortEdge > shortEdge {
			continue
		}

		resolved := template
		resolved.Width, resolved.Height = fitVariantDimensions(sourceWidth, sourceHeight, template.Width, template.Height)
		selected = append(selected, resolved)
	}
	if len(selected) > 0 || len(templates) == 0 {
		return selected
	}

	fallback := templates[len(templates)-1]
	fallback.Width, fallback.Height = fitVariantDimensions(sourceWidth, sourceHeight, fallback.Width, fallback.Height)

	return []TranscodeVariant{fallback}
}

func fitVariantDimensions(sourceWidth, sourceHeight, maxWidth, maxHeight int) (int, int) {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return maxWidth, maxHeight
	}

	limitWidth := sourceWidth
	if maxWidth < limitWidth {
		limitWidth = maxWidth
	}
	limitHeight := sourceHeight
	if maxHeight < limitHeight {
		limitHeight = maxHeight
	}

	sourceAspect := float64(sourceWidth) / float64(sourceHeight)
	maxAspect := float64(maxWidth) / float64(maxHeight)
	if sourceAspect >= maxAspect {
		width := evenAtMost(limitWidth)
		height := evenNearestAtMost(float64(width)/sourceAspect, limitHeight)
		if height > limitHeight {
			height = evenAtMost(limitHeight)
			width = evenNearestAtMost(float64(height)*sourceAspect, limitWidth)
		}
		return width, height
	}

	height := evenAtMost(limitHeight)
	width := evenNearestAtMost(float64(height)*sourceAspect, limitWidth)
	if width > limitWidth {
		width = evenAtMost(limitWidth)
		height = evenNearestAtMost(float64(width)/sourceAspect, limitHeight)
	}

	return width, height
}

func evenAtMost(value int) int {
	if value <= 2 {
		return 2
	}
	if value%2 != 0 {
		value--
	}

	return value
}

func evenFloor(value float64) int {
	n := int(math.Floor(value))
	if n <= 2 {
		return 2
	}
	if n%2 != 0 {
		n--
	}

	return n
}

func evenNearestAtMost(value float64, limit int) int {
	limit = evenAtMost(limit)
	if limit <= 2 {
		return 2
	}

	n := int(math.Round(value/2.0)) * 2
	if n <= 2 {
		return 2
	}
	if n > limit {
		return limit
	}

	return n
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

func audioInitPath(track AudioTrack) string {
	return filepathJoin("audio", track.ID, "init.mp4")
}

func audioPlaylistPath(track AudioTrack) string {
	return filepathJoin("audio", track.ID, "playlist.m3u8")
}

func audioSegmentPattern(track AudioTrack) string {
	return filepathJoin("audio", track.ID, "seg_$Number$.m4s")
}

func videoInitPath(variant TranscodeVariant) string {
	return filepathJoin("video", variant.Label, "init.mp4")
}

func videoPlaylistPath(variant TranscodeVariant) string {
	return filepathJoin("video", variant.Label, "playlist.m3u8")
}

func videoSegmentPattern(variant TranscodeVariant) string {
	return filepathJoin("video", variant.Label, "seg_$Number$.m4s")
}

func filepathJoin(parts ...string) string {
	return strings.Join(parts, "/")
}

func formatBandwidth(v TranscodeVariant) string {
	return strconv.Itoa(estimateBandwidth(v))
}

func audioName(track AudioTrack) string {
	if track.Role != "" {
		return strings.Title(track.Role)
	}
	return "Audio"
}

func keyHex(data []byte) string {
	return fmt.Sprintf("%x", data)
}

// ensureDir creates a directory if it does not exist.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
