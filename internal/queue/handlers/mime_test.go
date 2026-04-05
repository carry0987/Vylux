package handlers

import "testing"

func TestImageMimeType(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{format: "gif", want: "image/gif"},
		{format: "GIF", want: "image/gif"},
		{format: "webp", want: "image/webp"},
		{format: "jpg", want: "image/jpeg"},
		{format: "unknown", want: "application/octet-stream"},
	}

	for _, tc := range tests {
		if got := imageMimeType(tc.format); got != tc.want {
			t.Fatalf("imageMimeType(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}
