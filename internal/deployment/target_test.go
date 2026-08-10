package deployment

import (
	"errors"
	"testing"
)

func TestBackendIdentityMatchesHostStorageContract(t *testing.T) {
	tests := []struct {
		name string
		cfg  BackendConfig
		want string
	}{
		{
			name: "s3 includes provider endpoint region and bucket",
			cfg: BackendConfig{
				ProviderKind: "S3",
				Endpoint:     "HTTPS://user:pass@S3.EXAMPLE.TEST:443/account///?ignored=1#fragment",
				Region:       "ap-southeast-1",
				Bucket:       "source-bucket",
			},
			want: "sha256:v1:1bdcbf2a1b047489c4cd17011efdeccd8e291821d4814a6c0a1eebdbe01a60e5",
		},
		{
			name: "r2 excludes compatibility region",
			cfg: BackendConfig{
				ProviderKind: "r2",
				Endpoint:     "https://abc.r2.cloudflarestorage.com/",
				Region:       "auto",
				Bucket:       "media-bucket",
			},
			want: "sha256:v1:d3b6c930bb5825d525c7edaa2fee53c49b01a64f5a88b421a379ff5dea323b23",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BackendIdentity(tt.cfg)
			if err != nil {
				t.Fatalf("BackendIdentity: %v", err)
			}
			if got != tt.want {
				t.Fatalf("identity = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackendIdentityIncludesProviderKind(t *testing.T) {
	base := BackendConfig{
		Endpoint: "https://objects.example.test",
		Region:   "auto",
		Bucket:   "bucket",
	}
	s3 := base
	s3.ProviderKind = "s3"
	r2 := base
	r2.ProviderKind = "r2"

	s3Identity, err := BackendIdentity(s3)
	if err != nil {
		t.Fatalf("s3 identity: %v", err)
	}
	r2Identity, err := BackendIdentity(r2)
	if err != nil {
		t.Fatalf("r2 identity: %v", err)
	}
	if s3Identity == r2Identity {
		t.Fatal("provider migration must change backend identity")
	}
}

func TestTargetRequireRejectsEveryWrongTargetDimension(t *testing.T) {
	actual := testTarget(t, "550e8400-e29b-41d4-a716-446655440000", "source", "media")
	tests := []Target{
		{ProtocolVersion: 3, DeploymentID: actual.DeploymentID, SourceBackendIdentity: actual.SourceBackendIdentity, MediaBackendIdentity: actual.MediaBackendIdentity},
		testTarget(t, "550e8400-e29b-41d4-a716-446655440001", "source", "media"),
		testTarget(t, actual.DeploymentID, "other-source", "media"),
		testTarget(t, actual.DeploymentID, "source", "other-media"),
	}

	for _, expected := range tests {
		err := actual.Require(expected)
		if expected.ProtocolVersion == ProtocolVersion && !errors.Is(err, ErrTargetMismatch) {
			t.Fatalf("expected mismatch for %#v, got %v", expected, err)
		}
		if expected.ProtocolVersion != ProtocolVersion && err == nil {
			t.Fatal("unsupported protocol must fail")
		}
	}
}

func testTarget(t *testing.T, deploymentID, sourceBucket, mediaBucket string) Target {
	t.Helper()
	target, err := NewTarget(
		deploymentID,
		BackendConfig{ProviderKind: "s3", Endpoint: "https://source.example.test", Region: "test", Bucket: sourceBucket},
		BackendConfig{ProviderKind: "r2", Endpoint: "https://media.example.test", Region: "auto", Bucket: mediaBucket},
	)
	if err != nil {
		t.Fatalf("NewTarget: %v", err)
	}
	return target
}
