package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

const DefaultWaveformBins = 2048

// WaveformData is the JSON payload stored for waveform visualization.
type WaveformData struct {
	Bins []float32 `json:"bins"`
}

// GenerateWaveform extracts mono PCM samples and downsamples them into peak bins.
func GenerateWaveform(ctx context.Context, input string, bins int) (*WaveformData, error) {
	if bins <= 0 {
		bins = DefaultWaveformBins
	}

	args := []string{
		"-v", "error",
		"-i", input,
		"-map", "0:a:0",
		"-vn",
		"-ac", "1",
		"-acodec", "pcm_f32le",
		"-f", "f32le",
		"-",
	}

	data, err := ffmpeg(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("extract waveform samples: %w", err)
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("extract waveform samples: no audio samples returned")
	}

	sampleCount := len(data) / 4
	if sampleCount == 0 {
		return nil, fmt.Errorf("extract waveform samples: no decodable samples")
	}

	peaks := make([]float32, bins)
	for i := range sampleCount {
		bits := binary.LittleEndian.Uint32(data[i*4 : i*4+4])
		sample := math.Float32frombits(bits)
		amp := float32(math.Abs(float64(sample)))
		bin := i * bins / sampleCount
		if bin >= bins {
			bin = bins - 1
		}
		if amp > peaks[bin] {
			peaks[bin] = amp
		}
	}

	return &WaveformData{Bins: peaks}, nil
}
