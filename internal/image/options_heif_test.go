package image

import "testing"

func TestParseFormatRejectsHEIFOutputs(t *testing.T) {
	tests := []string{"heic", ".heic", "HEIF", ".heif"}

	for _, input := range tests {
		if got := ParseFormat(input); got != FormatOriginal {
			t.Fatalf("ParseFormat(%q) = %d, want %d", input, got, FormatOriginal)
		}
	}
}
