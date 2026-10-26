package image

import (
	"bytes"
	"errors"
	stdimage "image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"testing"

	"github.com/cshum/vipsgen/vips"
)

func TestMain(m *testing.M) {
	Startup()
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

func TestProcessPNGToAVIF(t *testing.T) {
	t.Helper()
	requireHEIFSupport(t)

	src := buildTestPNG(t)
	out, err := Process(src, Options{Format: FormatAVIF}, defaultTestLimits())
	if err != nil {
		t.Fatalf("Process PNG -> AVIF: %v", err)
	}

	assertDecodableImage(t, out, 2, 2)
}

func TestProcessAVIFInputToWebP(t *testing.T) {
	t.Helper()
	requireHEIFSupport(t)

	src := encodeHEIFBuffer(t, buildTestPNG(t), vips.HeifCompressionAv1)
	out, err := Process(src, Options{Format: FormatWebP}, defaultTestLimits())
	if err != nil {
		t.Fatalf("Process AVIF -> WebP: %v", err)
	}

	assertDecodableImage(t, out, 2, 2)
}

func TestProcessHEICInputToWebP(t *testing.T) {
	t.Helper()
	requireHEIFSupport(t)

	src := encodeHEIFBuffer(t, buildTestPNG(t), vips.HeifCompressionHevc)
	out, err := Process(src, Options{Format: FormatWebP}, defaultTestLimits())
	if err != nil {
		t.Fatalf("Process HEIC -> WebP: %v", err)
	}

	assertDecodableImage(t, out, 2, 2)
}

func TestProcess_RejectsAnimatedImageAboveConfiguredFrameLimit(t *testing.T) {
	limits := Limits{MaxAnimationFrames: 1}
	_, err := Process(buildTestGIF(t), Options{Format: FormatGIF}, limits)
	if !errors.Is(err, ErrDecodeImage) {
		t.Fatalf("Process animated GIF = %v, want decode error", err)
	}
}

func TestProcess_RejectsInvalidLimits(t *testing.T) {
	_, err := Process(buildTestPNG(t), Options{Format: FormatWebP}, Limits{})
	if !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("Process with invalid limits = %v, want invalid limits error", err)
	}
}

func defaultTestLimits() Limits {
	return Limits{MaxAnimationFrames: 1}
}

func buildTestPNG(t *testing.T) []byte {
	t.Helper()

	img := stdimage.NewNRGBA(stdimage.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	img.Set(1, 0, color.NRGBA{R: 0, G: 255, B: 0, A: 255})
	img.Set(0, 1, color.NRGBA{R: 0, G: 0, B: 255, A: 255})
	img.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 0, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}

	return buf.Bytes()
}

func buildTestGIF(t *testing.T) []byte {
	t.Helper()

	palette := color.Palette{
		color.Black,
		color.White,
	}
	frame := stdimage.NewPaletted(stdimage.Rect(0, 0, 1, 1), palette)
	frame.SetColorIndex(0, 0, 1)

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{
		Image: []*stdimage.Paletted{frame, frame},
		Delay: []int{1, 1},
	}); err != nil {
		t.Fatalf("encode test GIF: %v", err)
	}

	return buf.Bytes()
}

func requireHEIFSupport(t *testing.T) {
	t.Helper()

	probe := buildTestPNG(t)
	if _, err := encodeHEIFBufferForProbe(probe, vips.HeifCompressionAv1); err != nil {
		t.Skipf("libvips HEIF support unavailable: %v", err)
	}
}

func encodeHEIFBuffer(t *testing.T, src []byte, compression vips.HeifCompression) []byte {
	t.Helper()

	out, err := encodeHEIFBufferForProbe(src, compression)
	if err != nil {
		t.Fatalf("encode HEIF fixture: %v", err)
	}

	return out
}

func encodeHEIFBufferForProbe(src []byte, compression vips.HeifCompression) ([]byte, error) {
	img, err := vips.NewImageFromBuffer(src, nil)
	if err != nil {
		return nil, err
	}
	defer img.Close()

	return img.HeifsaveBuffer(&vips.HeifsaveBufferOptions{
		Q:           80,
		Compression: compression,
	})
}

func assertDecodableImage(t *testing.T, data []byte, wantWidth, wantHeight int) {
	t.Helper()

	img, err := vips.NewImageFromBuffer(data, nil)
	if err != nil {
		t.Fatalf("decode output image: %v", err)
	}
	defer img.Close()

	if img.Width() != wantWidth || img.Height() != wantHeight {
		t.Fatalf("output dimensions = %dx%d, want %dx%d", img.Width(), img.Height(), wantWidth, wantHeight)
	}
}
