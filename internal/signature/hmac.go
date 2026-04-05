package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Sign generates an HMAC-SHA256 signature for the given URL components.
// The signature covers both options and source to prevent URL tampering.
//
// URL format: /img/{signature}/{options}/{encoded_source}.{format}
func Sign(secret, options, source string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(options))
	mac.Write([]byte("/"))
	mac.Write([]byte(source))

	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks that the provided signature matches the expected HMAC-SHA256
// for the given options and source. Uses constant-time comparison to prevent
// timing attacks.
func Verify(secret, sig, options, source string) bool {
	expected := Sign(secret, options, source)

	return hmac.Equal([]byte(expected), []byte(sig))
}

// CanonicalizeObjectKey converts an incoming object key path into the stable
// decoded representation used for signing and storage lookups.
func CanonicalizeObjectKey(raw string) (string, error) {
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		return "", fmt.Errorf("missing object key")
	}

	key, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("invalid object key encoding: %w", err)
	}
	if key == "" {
		return "", fmt.Errorf("missing object key")
	}

	return key, nil
}

// CanonicalizeImageOptions normalizes an image options segment into a stable
// token order so equivalent requests produce the same signature input.
func CanonicalizeImageOptions(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	type optionValue struct {
		present bool
		value   int
	}

	var width, height, quality optionValue

	for _, part := range strings.Split(raw, "_") {
		if len(part) < 2 {
			return "", fmt.Errorf("invalid option token: %q", part)
		}

		prefix := part[0]
		value := part[1:]
		n, err := strconv.Atoi(value)
		if err != nil {
			return "", fmt.Errorf("invalid %c value: %q", prefix, value)
		}

		switch prefix {
		case 'w':
			if n < 0 {
				return "", fmt.Errorf("invalid width: %q", value)
			}
			width = optionValue{present: true, value: n}
		case 'h':
			if n < 0 {
				return "", fmt.Errorf("invalid height: %q", value)
			}
			height = optionValue{present: true, value: n}
		case 'q':
			if n < 1 || n > 100 {
				return "", fmt.Errorf("invalid quality: %q (must be 1-100)", value)
			}
			quality = optionValue{present: true, value: n}
		default:
			return "", fmt.Errorf("unknown option prefix: %q", string(prefix))
		}
	}

	parts := make([]string, 0, 3)
	if width.present {
		parts = append(parts, fmt.Sprintf("w%d", width.value))
	}
	if height.present {
		parts = append(parts, fmt.Sprintf("h%d", height.value))
	}
	if quality.present {
		parts = append(parts, fmt.Sprintf("q%d", quality.value))
	}

	return strings.Join(parts, "_"), nil
}

// CanonicalizeImageSourcePath converts an incoming image source path into the
// stable representation used for signing. The source key is treated as decoded
// logical data, while the output format is normalized to a canonical extension.
func CanonicalizeImageSourcePath(raw string) (string, error) {
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		return "", fmt.Errorf("missing source path")
	}

	ext := path.Ext(raw)
	if ext == "" {
		return "", fmt.Errorf("missing output format")
	}

	encodedSource := strings.TrimSuffix(raw, ext)
	sourceKey, err := url.PathUnescape(encodedSource)
	if err != nil {
		return "", fmt.Errorf("invalid source encoding: %w", err)
	}
	if sourceKey == "" {
		return "", fmt.Errorf("missing source key")
	}

	format := strings.ToLower(strings.TrimPrefix(ext, "."))
	if format == "jpeg" {
		format = "jpg"
	}
	if format == "" {
		return "", fmt.Errorf("missing output format")
	}

	return sourceKey + "." + format, nil
}

// SignImage canonicalizes image request inputs before computing the signature.
func SignImage(secret, rawOptions, rawSourcePath string) (string, error) {
	canonicalOptions, err := CanonicalizeImageOptions(rawOptions)
	if err != nil {
		return "", err
	}

	canonicalSource, err := CanonicalizeImageSourcePath(rawSourcePath)
	if err != nil {
		return "", err
	}

	return Sign(secret, canonicalOptions, canonicalSource), nil
}

// VerifyImage canonicalizes image request inputs before comparing signatures.
func VerifyImage(secret, sig, rawOptions, rawSourcePath string) (bool, error) {
	expected, err := SignImage(secret, rawOptions, rawSourcePath)
	if err != nil {
		return false, err
	}

	return hmac.Equal([]byte(expected), []byte(sig)), nil
}

// SignOriginal canonicalizes an original object key before computing the signature.
func SignOriginal(secret, rawObjectKey string) (string, error) {
	canonicalKey, err := CanonicalizeObjectKey(rawObjectKey)
	if err != nil {
		return "", err
	}

	return Sign(secret, "", canonicalKey), nil
}

// VerifyOriginal canonicalizes an original object key before comparing signatures.
func VerifyOriginal(secret, sig, rawObjectKey string) (bool, error) {
	expected, err := SignOriginal(secret, rawObjectKey)
	if err != nil {
		return false, err
	}

	return hmac.Equal([]byte(expected), []byte(sig)), nil
}

// SignWebhook generates an HMAC-SHA256 signature for a webhook request body.
// Used when Vylux sends callbacks to the main application.
//
// Header format: X-Signature: sha256={hex_digest}
func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook checks a webhook signature against the request body.
func VerifyWebhook(secret, sig string, body []byte) bool {
	expected := SignWebhook(secret, body)

	return hmac.Equal([]byte(expected), []byte(sig))
}

// SignThumb canonicalizes a media-bucket object key before computing the
// signature. The "thumb" domain prefix prevents cross-endpoint signature reuse
// between /thumb and /original.
func SignThumb(secret, rawObjectKey string) (string, error) {
	canonicalKey, err := CanonicalizeObjectKey(rawObjectKey)
	if err != nil {
		return "", err
	}

	return Sign(secret, "thumb", canonicalKey), nil
}

// VerifyThumb canonicalizes a media-bucket object key before comparing signatures.
func VerifyThumb(secret, sig, rawObjectKey string) (bool, error) {
	expected, err := SignThumb(secret, rawObjectKey)
	if err != nil {
		return false, err
	}

	return hmac.Equal([]byte(expected), []byte(sig)), nil
}
