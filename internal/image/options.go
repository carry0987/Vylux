package image

import (
	"fmt"
	"strconv"
	"strings"
)

// Format represents the output image format.
type Format int

const (
	FormatOriginal Format = iota
	FormatWebP
	FormatAVIF
	FormatJPEG
	FormatPNG
	FormatGIF
)

// String returns the MIME type for the format.
func (f Format) String() string {
	switch f {
	case FormatWebP:
		return "image/webp"
	case FormatAVIF:
		return "image/avif"
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	case FormatGIF:
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// Ext returns the file extension (without dot).
func (f Format) Ext() string {
	switch f {
	case FormatWebP:
		return "webp"
	case FormatAVIF:
		return "avif"
	case FormatJPEG:
		return "jpg"
	case FormatPNG:
		return "png"
	case FormatGIF:
		return "gif"
	default:
		return ""
	}
}

// SupportsAnimation reports whether the format can represent multi-frame animation.
func (f Format) SupportsAnimation() bool {
	return f == FormatWebP || f == FormatGIF
}

// ParseFormat converts a file extension string to a Format.
func ParseFormat(ext string) Format {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "webp":
		return FormatWebP
	case "avif":
		return FormatAVIF
	case "jpg", "jpeg":
		return FormatJPEG
	case "png":
		return FormatPNG
	case "gif":
		return FormatGIF
	default:
		return FormatOriginal
	}
}

// Options holds image processing parameters parsed from the URL.
// URL pattern: /img/{sig}/{options}/{encoded_source}.{format}
// Options segment: w300_h200_q80
type Options struct {
	Width   int    // w – target width  (0 = auto)
	Height  int    // h – target height (0 = auto)
	Quality int    // q – quality 1-100 (0 = default per format)
	Format  Format // derived from the URL extension
}

// DefaultQuality returns a sensible default quality for a given format.
func DefaultQuality(f Format) int {
	switch f {
	case FormatWebP:
		return 80
	case FormatAVIF:
		return 50
	case FormatJPEG:
		return 80
	case FormatPNG:
		return 0 // PNG is lossless, quality unused
	case FormatGIF:
		return 0 // GIF uses palette-based encoding, quality unused
	default:
		return 80
	}
}

// ParseOptions parses an options string like "w300_h200_q80" into Options.
func ParseOptions(raw string) (Options, error) {
	var opts Options

	if raw == "" {
		return opts, nil
	}

	for _, part := range strings.Split(raw, "_") {
		if len(part) < 2 {
			return opts, fmt.Errorf("invalid option token: %q", part)
		}

		prefix := part[0]
		value := part[1:]

		switch prefix {
		case 'w':
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return opts, fmt.Errorf("invalid width: %q", value)
			}

			opts.Width = n
		case 'h':
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return opts, fmt.Errorf("invalid height: %q", value)
			}

			opts.Height = n
		case 'q':
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 100 {
				return opts, fmt.Errorf("invalid quality: %q (must be 1-100)", value)
			}

			opts.Quality = n
		default:
			return opts, fmt.Errorf("unknown option prefix: %q", string(prefix))
		}
	}

	return opts, nil
}

// EffectiveQuality returns the quality to use: explicit if set, otherwise default.
func (o Options) EffectiveQuality() int {
	if o.Quality > 0 {
		return o.Quality
	}

	return DefaultQuality(o.Format)
}

// CacheKey returns a deterministic string for use as an LRU / S3 cache key.
// Format: "w{W}_h{H}_q{Q}.{ext}"
func (o Options) CacheKey(source string) string {
	return fmt.Sprintf("%s/w%d_h%d_q%d.%s", source, o.Width, o.Height, o.EffectiveQuality(), o.Format.Ext())
}
