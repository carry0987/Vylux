package deployment

import (
	"context"
	"fmt"

	"Vylux/internal/db/dbq"

	"github.com/jackc/pgx/v5/pgtype"
)

type targetQueries interface {
	BindMediaDeploymentTarget(context.Context, dbq.BindMediaDeploymentTargetParams) (int64, error)
	GetMediaDeploymentTarget(context.Context) (dbq.GetMediaDeploymentTargetRow, error)
}

func BindTarget(ctx context.Context, queries targetQueries, expected Target) (Target, error) {
	if queries == nil {
		return Target{}, fmt.Errorf("deployment target database is unavailable")
	}
	if err := expected.Validate(); err != nil {
		return Target{}, fmt.Errorf("invalid expected deployment target: %w", err)
	}

	_, err := queries.BindMediaDeploymentTarget(ctx, dbq.BindMediaDeploymentTargetParams{
		ProtocolVersion: pgtype.Int2{
			Int16: expected.ProtocolVersion,
			Valid: true,
		},
		DeploymentID: pgtype.Text{String: expected.DeploymentID, Valid: true},
		SourceBackendIdentity: pgtype.Text{
			String: expected.SourceBackendIdentity,
			Valid:  true,
		},
		MediaBackendIdentity: pgtype.Text{String: expected.MediaBackendIdentity, Valid: true},
	})
	if err != nil {
		return Target{}, fmt.Errorf("bind deployment target: %w", err)
	}

	row, err := queries.GetMediaDeploymentTarget(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read persisted deployment target: %w", err)
	}
	actual := Target{
		ProtocolVersion:       row.ProtocolVersion,
		DeploymentID:          row.DeploymentID,
		SourceBackendIdentity: row.SourceBackendIdentity,
		MediaBackendIdentity:  row.MediaBackendIdentity,
	}
	if err := actual.Validate(); err != nil {
		return Target{}, fmt.Errorf("persisted deployment target is invalid: %w", err)
	}
	if actual != expected {
		return Target{}, fmt.Errorf(
			"%w: configured deployment_id=%s does not match persisted deployment_id=%s",
			ErrTargetMismatch,
			expected.DeploymentID,
			actual.DeploymentID,
		)
	}

	return actual, nil
}
