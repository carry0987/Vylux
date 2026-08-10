package deployment

import (
	"context"
	"errors"
	"sync"
	"testing"

	"Vylux/internal/db/dbq"
)

type fakeTargetQueries struct {
	mu     sync.Mutex
	target *Target
}

func (q *fakeTargetQueries) BindMediaDeploymentTarget(
	_ context.Context,
	params dbq.BindMediaDeploymentTargetParams,
) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.target != nil {
		return 0, nil
	}
	if !params.ProtocolVersion.Valid || !params.DeploymentID.Valid ||
		!params.SourceBackendIdentity.Valid || !params.MediaBackendIdentity.Valid {
		return 0, errors.New("deployment target bind parameters must be non-null")
	}
	target := Target{
		ProtocolVersion:       params.ProtocolVersion.Int16,
		DeploymentID:          params.DeploymentID.String,
		SourceBackendIdentity: params.SourceBackendIdentity.String,
		MediaBackendIdentity:  params.MediaBackendIdentity.String,
	}
	q.target = &target
	return 1, nil
}

func (q *fakeTargetQueries) GetMediaDeploymentTarget(context.Context) (dbq.GetMediaDeploymentTargetRow, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.target == nil {
		return dbq.GetMediaDeploymentTargetRow{}, errors.New("target is unbound")
	}
	return dbq.GetMediaDeploymentTargetRow{
		ProtocolVersion:       q.target.ProtocolVersion,
		DeploymentID:          q.target.DeploymentID,
		SourceBackendIdentity: q.target.SourceBackendIdentity,
		MediaBackendIdentity:  q.target.MediaBackendIdentity,
	}, nil
}

func TestBindTargetAllowsSameTargetReplicas(t *testing.T) {
	queries := &fakeTargetQueries{}
	expected := testTarget(t, "550e8400-e29b-41d4-a716-446655440000", "source", "media")

	const replicas = 16
	errs := make(chan error, replicas)
	var wg sync.WaitGroup
	for range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actual, err := BindTarget(t.Context(), queries, expected)
			if err == nil && actual != expected {
				err = errors.New("replica observed a different target")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("same-target replica failed: %v", err)
		}
	}
}

func TestBindTargetRejectsConfigDriftWithoutRebinding(t *testing.T) {
	queries := &fakeTargetQueries{}
	original := testTarget(t, "550e8400-e29b-41d4-a716-446655440000", "source", "media")
	drifted := testTarget(t, original.DeploymentID, "source-after-migration", "media")

	if _, err := BindTarget(t.Context(), queries, original); err != nil {
		t.Fatalf("bind original target: %v", err)
	}
	if _, err := BindTarget(t.Context(), queries, drifted); !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("expected config drift mismatch, got %v", err)
	}

	actual, err := BindTarget(t.Context(), queries, original)
	if err != nil {
		t.Fatalf("rebind original target: %v", err)
	}
	if actual != original {
		t.Fatalf("drift attempt changed persisted target: %#v", actual)
	}
}
