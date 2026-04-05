package encryption

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"Vylux/internal/db/dbq"
)

const DefaultProtectionScheme = "cbcs"

// Material describes the encryption metadata required for raw-key CMAF packaging.
type Material struct {
	Key              []byte
	KeyID            []byte
	ProtectionScheme string
	KeyURI           string
}

// GenerateKey returns a random 16-byte AES-128 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate AES key: %w", err)
	}
	return key, nil
}

// SetupHLSEncryption generates a raw key + KID pair, persists the key metadata
// for later delivery, and returns the material needed by the packager.
func SetupHLSEncryption(
	ctx context.Context,
	hash string,
	baseURL string,
	queries *dbq.Queries,
	wrapper *KeyWrapper,
) (*Material, error) {
	aesKey, err := GenerateKey()
	if err != nil {
		return nil, err
	}

	kid, err := GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate KID: %w", err)
	}

	keyURI := strings.TrimRight(baseURL, "/") + "/api/key/" + hash
	wrappedKey, wrapNonce, kekVersion, err := wrapper.Wrap(aesKey)
	if err != nil {
		return nil, fmt.Errorf("wrap content key: %w", err)
	}
	if err := queries.UpsertEncryptionKey(ctx, dbq.UpsertEncryptionKeyParams{
		Hash:       hash,
		WrappedKey: wrappedKey,
		WrapNonce:  wrapNonce,
		KekVersion: kekVersion,
		Kid:        fmt.Sprintf("%x", kid),
		Scheme:     DefaultProtectionScheme,
		KeyUri:     keyURI,
	}); err != nil {
		return nil, fmt.Errorf("upsert encryption key: %w", err)
	}

	return &Material{
		Key:              aesKey,
		KeyID:            kid,
		ProtectionScheme: DefaultProtectionScheme,
		KeyURI:           keyURI,
	}, nil
}
