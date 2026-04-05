package image

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"github.com/cshum/vipsgen/vips"
)

var (
	ErrDecodeImage      = errors.New("decode image")
	ErrEncodeImage      = errors.New("encode image")
	ErrAnimatedToStatic = errors.New("animated image cannot be converted to static format (use webp or gif output)")
)

// MaxAnimationFrames is the safety limit on number of frames in an animated image.
const MaxAnimationFrames = 200

// inputSupportsAnimation checks whether the raw image bytes are GIF or WebP
// by inspecting magic bytes. Only these formats can carry multiple frames.
func inputSupportsAnimation(src []byte) bool {
	// GIF: starts with "GIF87a" or "GIF89a"
	if bytes.HasPrefix(src, []byte("GIF")) {
		return true
	}
	// WebP: starts with "RIFF....WEBP"
	if len(src) >= 12 && bytes.Equal(src[:4], []byte("RIFF")) && bytes.Equal(src[8:12], []byte("WEBP")) {
		return true
	}
	return false
}

// Startup initializes the libvips runtime. Call once at application start.
func Startup() {
	vips.Startup(nil)
}

// Shutdown releases libvips resources. Call once at application shutdown.
func Shutdown() {
	vips.Shutdown()
}

// Process takes raw image bytes and returns processed bytes according to opts.
// It uses ThumbnailBuffer for shrink-on-load when resizing is requested.
// For animated images, ThumbnailBuffer with Crop breaks the frame stack,
// so when both width and height are specified we use ThumbnailBuffer for
// cover-scaling then ExtractAreaMultiPage for per-frame centre crop.
func Process(src []byte, opts Options) ([]byte, error) {
	var img *vips.Image
	var err error

	// Only load all frames when both input and output support animation.
	// PNG/JPEG/AVIF loaders do not accept the "n" parameter.
	animatable := opts.Format.SupportsAnimation()
	loadAllFrames := animatable && inputSupportsAnimation(src)

	needResize := opts.Width > 0 || opts.Height > 0
	needCrop := opts.Width > 0 && opts.Height > 0

	if loadAllFrames && needCrop {
		// Animated + w+h: ThumbnailBuffer to cover size, then crop per-frame.
		img, err = animatedCoverCrop(src, opts.Width, opts.Height)
	} else if needResize {
		// Shrink-on-load: decode + resize in one pass.
		tbOpts := &vips.ThumbnailBufferOptions{
			Height: opts.Height,
		}
		if loadAllFrames {
			tbOpts.OptionString = "n=-1"
		} else {
			tbOpts.Crop = vips.InterestingCentre
		}
		img, err = vips.NewThumbnailBuffer(src, opts.Width, tbOpts)
	} else {
		// No resize requested — just decode.
		var loadOpts *vips.LoadOptions
		if loadAllFrames {
			loadOpts = &vips.LoadOptions{N: -1}
		}
		img, err = vips.NewImageFromBuffer(src, loadOpts)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeImage, err)
	}
	defer img.Close()

	// Reject animated input when output format cannot represent animation.
	if !animatable && img.Pages() > 1 {
		return nil, ErrAnimatedToStatic
	}

	// Safety limit: reject images with too many frames.
	if img.Pages() > MaxAnimationFrames {
		return nil, fmt.Errorf("%w: animated image exceeds frame limit (%d > %d)",
			ErrDecodeImage, img.Pages(), MaxAnimationFrames)
	}

	// Export to target format
	out, err := export(img, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncodeImage, err)
	}

	return out, nil
}

// animatedCoverCrop loads all animation frames, scales to cover the target
// dimensions, then centre-crops each frame with ExtractAreaMultiPage.
func animatedCoverCrop(src []byte, wantW, wantH int) (*vips.Image, error) {
	// Probe original dimensions for cover-scale calculation.
	probe, err := vips.NewImageFromBuffer(src, &vips.LoadOptions{N: -1})
	if err != nil {
		return nil, err
	}
	origW := probe.Width()
	origPageH := probe.PageHeight()
	probe.Close()

	coverScale := math.Max(float64(wantW)/float64(origW), float64(wantH)/float64(origPageH))
	coverW := int(float64(origW)*coverScale + 0.5)
	coverH := int(float64(origPageH)*coverScale + 0.5)

	img, err := vips.NewThumbnailBuffer(src, coverW, &vips.ThumbnailBufferOptions{
		Height:       coverH,
		OptionString: "n=-1",
	})
	if err != nil {
		return nil, err
	}

	// Centre crop each frame to exact target dimensions.
	cropLeft := (img.Width() - wantW) / 2
	cropTop := (img.PageHeight() - wantH) / 2
	if cropLeft < 0 {
		cropLeft = 0
	}
	if cropTop < 0 {
		cropTop = 0
	}
	if err := img.ExtractAreaMultiPage(cropLeft, cropTop, wantW, wantH); err != nil {
		img.Close()
		return nil, err
	}
	return img, nil
}

// export encodes the image into the requested format.
func export(img *vips.Image, opts Options) ([]byte, error) {
	q := opts.EffectiveQuality()
	animated := img.Pages() > 1

	switch opts.Format {
	case FormatWebP:
		webpOpts := &vips.WebpsaveBufferOptions{Q: q}
		if animated {
			webpOpts.PageHeight = img.PageHeight()
		}
		return img.WebpsaveBuffer(webpOpts)

	case FormatAVIF:
		return img.HeifsaveBuffer(&vips.HeifsaveBufferOptions{
			Q:           q,
			Compression: vips.HeifCompressionAv1,
		})

	case FormatJPEG:
		return img.JpegsaveBuffer(&vips.JpegsaveBufferOptions{
			Q:    q,
			Keep: vips.KeepNone,
		})

	case FormatPNG:
		return img.PngsaveBuffer(nil)

	case FormatGIF:
		gifOpts := &vips.GifsaveBufferOptions{}
		if animated {
			gifOpts.PageHeight = img.PageHeight()
		}
		return img.GifsaveBuffer(gifOpts)

	default:
		// Original format — re-export as the source format.
		// Try WebP as a safe default.
		return img.WebpsaveBuffer(&vips.WebpsaveBufferOptions{
			Q: 80,
		})
	}
}
