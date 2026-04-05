package image

import (
	"testing"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input string
		want  Format
	}{
		{"webp", FormatWebP},
		{".webp", FormatWebP},
		{"WEBP", FormatWebP},
		{"avif", FormatAVIF},
		{"jpg", FormatJPEG},
		{"jpeg", FormatJPEG},
		{".jpeg", FormatJPEG},
		{"png", FormatPNG},
		{"gif", FormatGIF},
		{".gif", FormatGIF},
		{"", FormatOriginal},
	}

	for _, tt := range tests {
		if got := ParseFormat(tt.input); got != tt.want {
			t.Errorf("ParseFormat(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFormatString(t *testing.T) {
	if got := FormatWebP.String(); got != "image/webp" {
		t.Errorf("FormatWebP.String() = %q", got)
	}

	if got := FormatAVIF.String(); got != "image/avif" {
		t.Errorf("FormatAVIF.String() = %q", got)
	}

	if got := FormatGIF.String(); got != "image/gif" {
		t.Errorf("FormatGIF.String() = %q", got)
	}
}

func TestFormatExt(t *testing.T) {
	if got := FormatJPEG.Ext(); got != "jpg" {
		t.Errorf("FormatJPEG.Ext() = %q", got)
	}

	if got := FormatPNG.Ext(); got != "png" {
		t.Errorf("FormatPNG.Ext() = %q", got)
	}

	if got := FormatGIF.Ext(); got != "gif" {
		t.Errorf("FormatGIF.Ext() = %q", got)
	}
}

func TestParseOptions(t *testing.T) {
	tests := []struct {
		input   string
		wantW   int
		wantH   int
		wantQ   int
		wantErr bool
	}{
		{"w300", 300, 0, 0, false},
		{"w300_h200", 300, 200, 0, false},
		{"w300_h200_q80", 300, 200, 80, false},
		{"q50", 0, 0, 50, false},
		{"", 0, 0, 0, false},
		{"w-1", 0, 0, 0, true},
		{"qabc", 0, 0, 0, true},
		{"q0", 0, 0, 0, true},
		{"q101", 0, 0, 0, true},
		{"x100", 0, 0, 0, true},
		{"w", 0, 0, 0, true},
	}

	for _, tt := range tests {
		opts, err := ParseOptions(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseOptions(%q) expected error", tt.input)
			}

			continue
		}

		if err != nil {
			t.Errorf("ParseOptions(%q) unexpected error: %v", tt.input, err)
			continue
		}

		if opts.Width != tt.wantW || opts.Height != tt.wantH || opts.Quality != tt.wantQ {
			t.Errorf("ParseOptions(%q) = {W:%d H:%d Q:%d}, want {W:%d H:%d Q:%d}",
				tt.input, opts.Width, opts.Height, opts.Quality, tt.wantW, tt.wantH, tt.wantQ)
		}
	}
}

func TestEffectiveQuality(t *testing.T) {
	opts := Options{Quality: 0, Format: FormatWebP}
	if got := opts.EffectiveQuality(); got != 80 {
		t.Errorf("default webp quality = %d, want 80", got)
	}

	opts.Quality = 42
	if got := opts.EffectiveQuality(); got != 42 {
		t.Errorf("explicit quality = %d, want 42", got)
	}
}

func TestCacheKey(t *testing.T) {
	opts := Options{Width: 300, Height: 200, Quality: 80, Format: FormatWebP}
	key := opts.CacheKey("media/uploads/abc.jpg")

	want := "media/uploads/abc.jpg/w300_h200_q80.webp"
	if key != want {
		t.Errorf("CacheKey = %q, want %q", key, want)
	}
}
