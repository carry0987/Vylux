package encryption

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"uuid"

	"Vylux/internal/db/dbq"
)

const DefaultProtectionScheme = "cbcs"

const (
	AssetTypeAudio   = "audio"
	AssetTypeVideo   = "video"
	PackagingTypeHLS = "hls"
)

// Material describes the encryption metadata required for raw-key CMAF packaging.
type Material struct {
	ID               uuid.UUID
	Key              []byte
	KeyID            []byte
	ProtectionScheme string
	KeyURI           string
}

func KeyURI(baseURL string, id uuid.UUID) string {
	return strings.TrimRight(baseURL, "/") + "/api/key/" + id.String()
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
	assetType string,
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

	wrappedKey, wrapNonce, kekVersion, err := wrapper.Wrap(aesKey)
	if err != nil {
		return nil, fmt.Errorf("wrap content key: %w", err)
	}
	row, err := queries.UpsertStreamEncryptionKey(ctx, dbq.UpsertStreamEncryptionKeyParams{
		ID:            uuid.New(),
		SourceHash:    hash,
		AssetType:     assetType,
		PackagingType: PackagingTypeHLS,
		WrappedKey:    wrappedKey,
		WrapNonce:     wrapNonce,
		KekVersion:    kekVersion,
		Kid:           fmt.Sprintf("%x", kid),
		Scheme:        DefaultProtectionScheme,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert encryption key: %w", err)
	}

	return &Material{
		ID:               row.ID,
		Key:              aesKey,
		KeyID:            kid,
		ProtectionScheme: DefaultProtectionScheme,
		KeyURI:           KeyURI(baseURL, row.ID),
	}, nil
}
