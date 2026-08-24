package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"
	"strings"

	"Vylux/internal/mediaexec"
)

var ffprobePath = "ffprobe"

// SetFFprobePath overrides the default ffprobe binary location.
func SetFFprobePath(path string) {
	if path != "" {
		ffprobePath = path
	}
}

// SourceFormat identifies the source audio format inferred from the file path.
type SourceFormat string

const (
	FormatUnknown SourceFormat = "unknown"
	FormatAAC     SourceFormat = "aac"
	FormatAIFF    SourceFormat = "aiff"
	FormatALAC    SourceFormat = "alac"
	FormatFLAC    SourceFormat = "flac"
	FormatM4A     SourceFormat = "m4a"
	FormatMP3     SourceFormat = "mp3"
	FormatOGG     SourceFormat = "ogg"
	FormatOpus    SourceFormat = "opus"
	FormatWAV     SourceFormat = "wav"
)

// Supported reports whether the source format is accepted by the audio pipeline.
func (f SourceFormat) Supported() bool {
	switch f {
	case FormatAAC, FormatAIFF, FormatALAC, FormatFLAC, FormatM4A, FormatMP3, FormatOGG, FormatOpus, FormatWAV:
		return true
	default:
		return false
	}
}

// ProbeResult describes the source container and audio streams.
type ProbeResult struct {
	SourceFormat SourceFormat
	Container    string
	DurationSec  float64
	Bitrate      int
	Streams      []AudioStream
	Tags         map[string]string
}

// AudioStream describes one audio stream in the source.
type AudioStream struct {
	Index         int
	Codec         string
	SampleRate    int
	Channels      int
	ChannelLayout string
	Bitrate       int
	BitsPerSample int
	Tags          map[string]string
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeFormat struct {
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	BitRate    string            `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type ffprobeStream struct {
	Index         int               `json:"index"`
	CodecName     string            `json:"codec_name"`
	CodecType     string            `json:"codec_type"`
	SampleRate    string            `json:"sample_rate"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	BitRate       string            `json:"bit_rate"`
	BitsPerSample int               `json:"bits_per_sample"`
	Tags          map[string]string `json:"tags"`
}

// DetectSourceFormat infers the input format from the file extension.
func DetectSourceFormat(path string) SourceFormat {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "aac":
		return FormatAAC
	case "aif", "aiff":
		return FormatAIFF
	case "alac":
		return FormatALAC
	case "flac":
		return FormatFLAC
	case "m4a":
		return FormatM4A
	case "mp3":
		return FormatMP3
	case "ogg":
		return FormatOGG
	case "opus":
		return FormatOpus
	case "wav", "wave":
		return FormatWAV
	default:
		return FormatUnknown
	}
}

// Probe inspects an audio source with ffprobe.
func Probe(ctx context.Context, input string) (*ProbeResult, error) {
	out, err := mediaexec.New(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		input,
	).Output()
	if err != nil {
		return nil, err
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse ffprobe audio probe: %w", err)
	}

	result := &ProbeResult{
		SourceFormat: DetectSourceFormat(input),
		Container:    parsed.Format.FormatName,
		DurationSec:  parseFloat(parsed.Format.Duration),
		Bitrate:      parseInt(parsed.Format.BitRate),
		Tags:         cloneTags(parsed.Format.Tags),
		Streams:      make([]AudioStream, 0, len(parsed.Streams)),
	}

	for _, stream := range parsed.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		result.Streams = append(result.Streams, AudioStream{
			Index:         stream.Index,
			Codec:         stream.CodecName,
			SampleRate:    parseInt(stream.SampleRate),
			Channels:      stream.Channels,
			ChannelLayout: stream.ChannelLayout,
			Bitrate:       parseInt(stream.BitRate),
			BitsPerSample: stream.BitsPerSample,
			Tags:          cloneTags(stream.Tags),
		})
	}

	if len(result.Streams) == 0 {
		return nil, fmt.Errorf("no audio stream found")
	}

	if result.SourceFormat == FormatUnknown {
		result.SourceFormat = inferProbeFormat(result.Container, result.Streams)
	}

	return result, nil
}

func inferProbeFormat(container string, streams []AudioStream) SourceFormat {
	container = strings.ToLower(strings.TrimSpace(container))
	if strings.Contains(container, "flac") {
		return FormatFLAC
	}
	if strings.Contains(container, "wav") {
		return FormatWAV
	}
	if strings.Contains(container, "aiff") {
		return FormatAIFF
	}
	if strings.Contains(container, "mp3") {
		return FormatMP3
	}
	if strings.Contains(container, "ogg") {
		return FormatOGG
	}
	for _, stream := range streams {
		switch strings.ToLower(strings.TrimSpace(stream.Codec)) {
		case "opus":
			return FormatOpus
		case "alac":
			return FormatALAC
		case "aac":
			return FormatAAC
		}
	}
	if strings.Contains(container, "mov") || strings.Contains(container, "mp4") {
		return FormatM4A
	}

	return FormatUnknown
}

func parseInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}

	return value
}

func parseFloat(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}

	return value
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(tags))
	maps.Copy(cloned, tags)

	return cloned
}
