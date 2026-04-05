package encryption

import (
	"bytes"
	"strings"
	"testing"
)

func TestKeyWrapperWrapRoundTrip(t *testing.T) {
	wrapper, err := NewKeyWrapper(strings.Repeat("a1", 32))
	if err != nil {
		t.Fatalf("NewKeyWrapper: %v", err)
	}

	plaintext := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}
	wrapped, nonce, version, err := wrapper.Wrap(plaintext)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if bytes.Equal(wrapped, plaintext) {
		t.Fatal("wrapped key should not equal plaintext")
	}

	got, err := wrapper.Unwrap(wrapped, nonce, version)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Unwrap = %x, want %x", got, plaintext)
	}
}

func TestKeyWrapperRejectsWrongVersion(t *testing.T) {
	wrapper, err := NewKeyWrapper(strings.Repeat("b2", 32))
	if err != nil {
		t.Fatalf("NewKeyWrapper: %v", err)
	}

	wrapped, nonce, _, err := wrapper.Wrap([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}

	if _, err := wrapper.Unwrap(wrapped, nonce, "v2"); err == nil {
		t.Fatal("expected unsupported version error")
	}
}
