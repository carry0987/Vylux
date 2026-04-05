package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const CurrentKEKVersion = "v1"

// KeyWrapper wraps per-video content keys before they are stored in PostgreSQL.
type KeyWrapper struct {
	aead    cipher.AEAD
	version string
}

// NewKeyWrapper constructs a wrapper from a 32-byte hex-encoded KEK.
func NewKeyWrapper(hexKey string) (*KeyWrapper, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}

	return &KeyWrapper{aead: aead, version: CurrentKEKVersion}, nil
}

// Wrap seals a plaintext content key for storage.
func (w *KeyWrapper) Wrap(plaintext []byte) (wrapped []byte, nonce []byte, version string, err error) {
	nonce = make([]byte, w.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", fmt.Errorf("generate GCM nonce: %w", err)
	}

	wrapped = w.aead.Seal(nil, nonce, plaintext, []byte(w.version))
	return wrapped, nonce, w.version, nil
}

// Unwrap restores a plaintext content key from storage.
func (w *KeyWrapper) Unwrap(wrapped, nonce []byte, version string) ([]byte, error) {
	if version != w.version {
		return nil, fmt.Errorf("unsupported KEK version %q", version)
	}

	plaintext, err := w.aead.Open(nil, nonce, wrapped, []byte(version))
	if err != nil {
		return nil, fmt.Errorf("unwrap content key: %w", err)
	}

	return plaintext, nil
}
