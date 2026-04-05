package video

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ProgressFunc is called with the current progress (0.0 – 1.0) during transcoding.
type ProgressFunc func(percent float64)

// timeRe matches FFmpeg's "time=HH:MM:SS.mm" stderr output.
var timeRe = regexp.MustCompile(`time=(\d{2}):(\d{2}):(\d{2})\.(\d{2})`)

// ParseProgress reads FFmpeg stderr and calls fn with the progress ratio.
// totalDuration is the expected input duration in seconds.
func ParseProgress(r io.Reader, totalDuration float64, fn ProgressFunc) {
	if totalDuration <= 0 || fn == nil {
		return
	}

	scanner := bufio.NewScanner(r)
	scanner.Split(scanFFmpegLines)

	for scanner.Scan() {
		line := scanner.Text()

		matches := timeRe.FindStringSubmatch(line)
		if len(matches) != 5 {
			continue
		}

		h, _ := strconv.Atoi(matches[1])
		m, _ := strconv.Atoi(matches[2])
		s, _ := strconv.Atoi(matches[3])
		cs, _ := strconv.Atoi(matches[4]) // centiseconds

		elapsed := time.Duration(h)*time.Hour +
			time.Duration(m)*time.Minute +
			time.Duration(s)*time.Second +
			time.Duration(cs)*10*time.Millisecond

		pct := elapsed.Seconds() / totalDuration
		if pct > 1.0 {
			pct = 1.0
		}

		fn(pct)
	}
}

// FormatDuration returns "HH:MM:SS" from seconds.
func FormatDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// scanFFmpegLines is a bufio.SplitFunc that splits on \r or \n.
// FFmpeg uses \r for progress lines.
func scanFFmpegLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}

	if atEOF {
		return len(data), data, nil
	}

	return 0, nil, nil
}

// ParseDurationFromProbe extracts duration from ffprobe output string.
// Accepts formats like "01:23:45.67" or "5045.67".
func ParseDurationFromProbe(s string) (float64, error) {
	s = strings.TrimSpace(s)

	// Try HH:MM:SS.cc format first.
	matches := timeRe.FindStringSubmatch("time=" + s)
	if len(matches) == 5 {
		h, _ := strconv.ParseFloat(matches[1], 64)
		m, _ := strconv.ParseFloat(matches[2], 64)
		sec, _ := strconv.ParseFloat(matches[3], 64)
		cs, _ := strconv.ParseFloat(matches[4], 64)

		return h*3600 + m*60 + sec + cs/100, nil
	}

	// Plain seconds.
	return strconv.ParseFloat(s, 64)
}
