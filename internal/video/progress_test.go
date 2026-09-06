package video

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseProgressParsesCarriageReturnUpdates(t *testing.T) {
	input := strings.NewReader("frame=1 time=00:00:01.00\rframe=2 time=00:00:02.50\r")
	var got []float64

	err := ParseProgress(input, 4, func(percent float64) {
		got = append(got, percent)
	})
	if err != nil {
		t.Fatalf("ParseProgress: %v", err)
	}

	want := []float64{0.25, 0.625}
	if !slices.Equal(got, want) {
		t.Fatalf("progress updates = %v, want %v", got, want)
	}
}

func TestParseProgressReturnsScannerError(t *testing.T) {
	boom := errors.New("boom")
	reader := &errorAfterReader{
		data: []byte("frame=1 time=00:00:01.00\r"),
		err:  boom,
	}

	err := ParseProgress(reader, 4, func(float64) {})
	if err == nil {
		t.Fatal("expected scanner error")
	}
	if !strings.Contains(err.Error(), "scan ffmpeg progress") {
		t.Fatalf("error = %v, want wrapped scan error", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
}

type errorAfterReader struct {
	data []byte
	err  error
	read bool
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.err
	}
	r.read = true
	n := copy(p, r.data)
	if n < len(r.data) {
		r.data = r.data[n:]
		r.read = false
	}
	return n, nil
}
