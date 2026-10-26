package encryption

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const testImageSourceKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestImageSourceCipherDisabled(t *testing.T) {
	cipher, err := NewImageSourceCipher("")
	if err != nil {
		t.Fatalf("NewImageSourceCipher: %v", err)
	}
	if cipher.Enabled() {
		t.Fatal("expected image source encryption to be disabled")
	}
	if _, err := cipher.Encrypt("uploads/image.png"); !errors.Is(err, ErrImageSourceEncryptionOff) {
		t.Fatalf("Encrypt error = %v, want ErrImageSourceEncryptionOff", err)
	}
}

func TestNewImageSourceCipherRejectsInvalidKeys(t *testing.T) {
	for _, key := range []string{"not-hex", "0011", strings.Repeat("a", 66)} {
		t.Run(key, func(t *testing.T) {
			if _, err := NewImageSourceCipher(key); err == nil {
				t.Fatal("expected invalid key error")
			}
		})
	}
}

func TestImageSourceCipherRoundTrip(t *testing.T) {
	cipher, err := NewImageSourceCipher(testImageSourceKey)
	if err != nil {
		t.Fatalf("NewImageSourceCipher: %v", err)
	}

	sourceKey := "static/論壇/recommend cover.png"
	first, err := cipher.Encrypt(sourceKey)
	if err != nil {
		t.Fatalf("Encrypt first token: %v", err)
	}
	second, err := cipher.Encrypt(sourceKey)
	if err != nil {
		t.Fatalf("Encrypt second token: %v", err)
	}
	if first == second {
		t.Fatal("expected random nonces to produce different tokens")
	}

	for _, token := range []string{first, second} {
		got, err := cipher.Decrypt(token)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", token, err)
		}
		if got != sourceKey {
			t.Fatalf("Decrypt(%q) = %q, want %q", token, got, sourceKey)
		}
	}
}

func TestImageSourceCipherRejectsInvalidSources(t *testing.T) {
	cipher, err := NewImageSourceCipher(testImageSourceKey)
	if err != nil {
		t.Fatalf("NewImageSourceCipher: %v", err)
	}

	if _, err := cipher.Encrypt(""); !errors.Is(err, ErrInvalidImageSource) {
		t.Fatalf("Encrypt empty source error = %v, want ErrInvalidImageSource", err)
	}
	if _, err := cipher.Encrypt(strings.Repeat("a", maxImageSourceKeyBytes+1)); !errors.Is(err, ErrInvalidImageSource) {
		t.Fatalf("Encrypt oversized source error = %v, want ErrInvalidImageSource", err)
	}
}

func TestImageSourceCipherRejectsInvalidTokens(t *testing.T) {
	cipher, err := NewImageSourceCipher(testImageSourceKey)
	if err != nil {
		t.Fatalf("NewImageSourceCipher: %v", err)
	}
	token, err := cipher.Encrypt("uploads/image.png")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 1
	wrongVersion := append([]byte(nil), raw...)
	wrongVersion[0]++

	tests := []string{
		"",
		"not+base64url",
		base64.RawURLEncoding.EncodeToString(raw[:10]),
		base64.RawURLEncoding.EncodeToString(tampered),
		base64.RawURLEncoding.EncodeToString(wrongVersion),
	}
	for _, invalid := range tests {
		if _, err := cipher.Decrypt(invalid); !errors.Is(err, ErrInvalidImageSourceToken) {
			t.Fatalf("Decrypt(%q) error = %v, want ErrInvalidImageSourceToken", invalid, err)
		}
	}
}

func TestImageSourceCipherRejectsAnotherKey(t *testing.T) {
	first, err := NewImageSourceCipher(testImageSourceKey)
	if err != nil {
		t.Fatalf("NewImageSourceCipher first: %v", err)
	}
	second, err := NewImageSourceCipher(strings.Repeat("f", 64))
	if err != nil {
		t.Fatalf("NewImageSourceCipher second: %v", err)
	}
	token, err := first.Encrypt("uploads/image.png")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := second.Decrypt(token); !errors.Is(err, ErrInvalidImageSourceToken) {
		t.Fatalf("Decrypt with another key error = %v, want ErrInvalidImageSourceToken", err)
	}
}

func TestImageSourceCipherCrossLanguageVector(t *testing.T) {
	cipher, err := NewImageSourceCipher(testImageSourceKey)
	if err != nil {
		t.Fatalf("NewImageSourceCipher: %v", err)
	}
	nonce, err := hex.DecodeString("000102030405060708090a0b")
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}

	got, err := cipher.encryptWithNonce("static/forum/recommend-cover.png", nonce)
	if err != nil {
		t.Fatalf("encryptWithNonce: %v", err)
	}
	const want = "AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg"
	if got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
}
