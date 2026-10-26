package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	imageSourceTokenVersion byte = 1
	maxImageSourceKeyBytes  int  = 1024
)

var (
	imageSourceAAD              = []byte("vylux/image-source/v1")
	ErrImageSourceEncryptionOff = errors.New("image source encryption is disabled")
	ErrInvalidImageSource       = errors.New("invalid image source key")
	ErrInvalidImageSourceToken  = errors.New("invalid image source token")
)

// ImageSourceCipher encrypts source object keys into opaque URL-safe tokens.
type ImageSourceCipher struct {
	aead cipher.AEAD
}

// NewImageSourceCipher constructs an optional AES-256-GCM source-key cipher.
func NewImageSourceCipher(hexKey string) (*ImageSourceCipher, error) {
	if hexKey == "" {
		return &ImageSourceCipher{}, nil
	}

	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode IMAGE_SOURCE_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("IMAGE_SOURCE_KEY must decode to 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create image source AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create image source AES-GCM: %w", err)
	}

	return &ImageSourceCipher{aead: aead}, nil
}

// Enabled reports whether encrypted source tokens are required.
func (c *ImageSourceCipher) Enabled() bool {
	return c != nil && c.aead != nil
}

// Encrypt returns a versioned, unpadded Base64URL token for sourceKey.
func (c *ImageSourceCipher) Encrypt(sourceKey string) (string, error) {
	if !c.Enabled() {
		return "", ErrImageSourceEncryptionOff
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate image source nonce: %w", err)
	}

	return c.encryptWithNonce(sourceKey, nonce)
}

func (c *ImageSourceCipher) encryptWithNonce(sourceKey string, nonce []byte) (string, error) {
	if !c.Enabled() {
		return "", ErrImageSourceEncryptionOff
	}
	if len(sourceKey) == 0 || len(sourceKey) > maxImageSourceKeyBytes {
		return "", ErrInvalidImageSource
	}
	if len(nonce) != c.aead.NonceSize() {
		return "", fmt.Errorf("invalid image source nonce length: %d", len(nonce))
	}

	token := make([]byte, 1, 1+len(nonce)+len(sourceKey)+c.aead.Overhead())
	token[0] = imageSourceTokenVersion
	token = append(token, nonce...)
	token = c.aead.Seal(token, nonce, []byte(sourceKey), imageSourceAAD)

	return base64.RawURLEncoding.EncodeToString(token), nil
}

// Decrypt authenticates a source token and returns the exact source object key.
func (c *ImageSourceCipher) Decrypt(encodedToken string) (string, error) {
	if !c.Enabled() {
		return "", ErrImageSourceEncryptionOff
	}

	maxTokenBytes := 1 + c.aead.NonceSize() + maxImageSourceKeyBytes + c.aead.Overhead()
	if encodedToken == "" || len(encodedToken) > base64.RawURLEncoding.EncodedLen(maxTokenBytes) {
		return "", ErrInvalidImageSourceToken
	}

	token, err := base64.RawURLEncoding.DecodeString(encodedToken)
	if err != nil {
		return "", ErrInvalidImageSourceToken
	}
	minimumLength := 1 + c.aead.NonceSize() + c.aead.Overhead()
	if len(token) <= minimumLength || token[0] != imageSourceTokenVersion {
		return "", ErrInvalidImageSourceToken
	}

	nonceEnd := 1 + c.aead.NonceSize()
	nonce := token[1:nonceEnd]
	plaintext, err := c.aead.Open(nil, nonce, token[nonceEnd:], imageSourceAAD)
	if err != nil || len(plaintext) == 0 || len(plaintext) > maxImageSourceKeyBytes {
		return "", ErrInvalidImageSourceToken
	}

	return string(plaintext), nil
}
