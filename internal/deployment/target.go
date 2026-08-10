package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	ProtocolVersion int16 = 2

	HeaderProtocolVersion       = "X-Vylux-Protocol-Version"
	HeaderDeploymentID          = "X-Vylux-Deployment-ID"
	HeaderSourceBackendIdentity = "X-Vylux-Source-Backend-Identity"
	HeaderMediaBackendIdentity  = "X-Vylux-Media-Backend-Identity"
)

var (
	ErrTargetMismatch = errors.New("Vylux deployment target mismatch")
	identityPattern   = regexp.MustCompile(`^sha256:v1:[0-9a-f]{64}$`)
)

type BackendConfig struct {
	ProviderKind string
	Endpoint     string
	Region       string
	Bucket       string
}

type Target struct {
	ProtocolVersion       int16  `json:"protocol_version"`
	DeploymentID          string `json:"deployment_id"`
	SourceBackendIdentity string `json:"source_backend_identity"`
	MediaBackendIdentity  string `json:"media_backend_identity"`
}

func NewTarget(deploymentID string, source, media BackendConfig) (Target, error) {
	parsedID, err := uuid.Parse(strings.TrimSpace(deploymentID))
	if err != nil || parsedID == uuid.Nil {
		return Target{}, fmt.Errorf("DEPLOYMENT_ID must be a non-zero UUID")
	}

	sourceIdentity, err := BackendIdentity(source)
	if err != nil {
		return Target{}, fmt.Errorf("source backend identity: %w", err)
	}
	mediaIdentity, err := BackendIdentity(media)
	if err != nil {
		return Target{}, fmt.Errorf("media backend identity: %w", err)
	}

	return Target{
		ProtocolVersion:       ProtocolVersion,
		DeploymentID:          parsedID.String(),
		SourceBackendIdentity: sourceIdentity,
		MediaBackendIdentity:  mediaIdentity,
	}, nil
}

// BackendIdentity mirrors the host storage identity contract. Credentials and
// public delivery URLs are intentionally excluded so key rotation is safe.
func BackendIdentity(cfg BackendConfig) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.ProviderKind))
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return "", err
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return "", fmt.Errorf("bucket is required")
	}

	var parts []string
	switch provider {
	case "s3":
		region := strings.TrimSpace(cfg.Region)
		if region == "" {
			return "", fmt.Errorf("region is required for s3")
		}
		parts = []string{provider, endpoint, region, bucket}
	case "r2":
		parts = []string{provider, endpoint, bucket}
	default:
		return "", fmt.Errorf("provider kind must be s3 or r2, got %q", cfg.ProviderKind)
	}

	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(parts); err != nil {
		return "", fmt.Errorf("encode backend identity: %w", err)
	}
	payload := bytes.TrimSuffix(canonical.Bytes(), []byte{'\n'})
	sum := sha256.Sum256(payload)

	return "sha256:v1:" + hex.EncodeToString(sum[:]), nil
}

func (t Target) Validate() error {
	if t.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("protocol_version must be %d", ProtocolVersion)
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(t.DeploymentID))
	if err != nil || parsedID == uuid.Nil || parsedID.String() != t.DeploymentID {
		return fmt.Errorf("deployment_id must be a canonical non-zero UUID")
	}
	if !identityPattern.MatchString(t.SourceBackendIdentity) {
		return fmt.Errorf("source_backend_identity must be a sha256:v1 fingerprint")
	}
	if !identityPattern.MatchString(t.MediaBackendIdentity) {
		return fmt.Errorf("media_backend_identity must be a sha256:v1 fingerprint")
	}
	return nil
}

func (t Target) Require(expected Target) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("configured deployment target is unavailable: %w", err)
	}
	if err := expected.Validate(); err != nil {
		return fmt.Errorf("invalid expected deployment target: %w", err)
	}
	if t != expected {
		return fmt.Errorf("%w", ErrTargetMismatch)
	}
	return nil
}

func (t Target) SetHeaders(header http.Header) {
	if err := t.Validate(); err != nil {
		return
	}
	header.Set(HeaderProtocolVersion, strconv.Itoa(int(t.ProtocolVersion)))
	header.Set(HeaderDeploymentID, t.DeploymentID)
	header.Set(HeaderSourceBackendIdentity, t.SourceBackendIdentity)
	header.Set(HeaderMediaBackendIdentity, t.MediaBackendIdentity)
}

func normalizeEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("endpoint must be a valid URL: %w", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint must use http:// or https://")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("endpoint must include a host")
	}

	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		if strings.Contains(hostname, ":") {
			parsed.Host = "[" + hostname + "]"
		} else {
			parsed.Host = hostname
		}
	} else {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")

	return parsed.String(), nil
}
