package integration

import (
	"context"
	"strings"
	"testing"

	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/migrations"
	"Vylux/tests/testutil"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestLifecycleSchemaFreshMigrationAndBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	postgres := testutil.StartPostgres(ctx, t)
	if err := db.Migrate(ctx, postgres.DSN, migrations.FS); err != nil {
		t.Fatalf("migrate fresh database: %v", err)
	}
	if err := db.Migrate(ctx, postgres.DSN, migrations.FS); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}

	pool, err := db.Connect(ctx, postgres.DSN)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)
	queries := dbq.New(pool)

	readiness, err := queries.GetMediaLifecycleReadiness(ctx)
	if err != nil {
		t.Fatalf("read lifecycle readiness: %v", err)
	}
	if !readiness.Singleton || readiness.CacheAuditArmed || readiness.CacheAuditComplete {
		t.Fatalf("unexpected readiness defaults: %+v", readiness)
	}
	unbound, err := queries.GetMediaDeploymentTarget(ctx)
	if err != nil {
		t.Fatalf("read unbound deployment target: %v", err)
	}
	if unbound.ProtocolVersion != 0 || unbound.DeploymentID != "" || unbound.SourceBackendIdentity != "" ||
		unbound.MediaBackendIdentity != "" {
		t.Fatalf("fresh deployment target is unexpectedly bound: %+v", unbound)
	}

	const (
		hash   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		source = "uploads/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-file.png"
		other  = "imports/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-copy.png"
	)
	if err := queries.CreateMediaTombstone(ctx, dbq.CreateMediaTombstoneParams{Hash: hash, Source: source}); err != nil {
		t.Fatalf("create media tombstone: %v", err)
	}
	if err := queries.CreateMediaTombstone(ctx, dbq.CreateMediaTombstoneParams{Hash: hash, Source: source}); err != nil {
		t.Fatalf("re-create media tombstone: %v", err)
	}
	tombstoned, err := queries.IsMediaTombstoned(ctx, dbq.IsMediaTombstonedParams{Hash: hash, Source: source})
	if err != nil {
		t.Fatalf("read media tombstone: %v", err)
	}
	if !tombstoned {
		t.Fatal("expected exact source to remain tombstoned")
	}
	if err := queries.CreateMediaTombstone(ctx, dbq.CreateMediaTombstoneParams{Hash: hash, Source: other}); err != nil {
		t.Fatalf("create same-hash tombstone for another source: %v", err)
	}
	otherTombstoned, err := queries.IsMediaTombstoned(ctx, dbq.IsMediaTombstonedParams{Hash: hash, Source: other})
	if err != nil {
		t.Fatalf("read same-hash tombstone for another source: %v", err)
	}
	if !otherTombstoned {
		t.Fatal("expected tombstone identity to include the exact source")
	}

	target := dbq.BindMediaDeploymentTargetParams{
		ProtocolVersion:       pgtype.Int2{Int16: 2, Valid: true},
		DeploymentID:          pgtype.Text{String: "11111111-1111-4111-8111-111111111111", Valid: true},
		SourceBackendIdentity: pgtype.Text{String: "sha256:v1:" + hash, Valid: true},
		MediaBackendIdentity:  pgtype.Text{String: "sha256:v1:" + hash, Valid: true},
	}
	rows, err := queries.BindMediaDeploymentTarget(ctx, target)
	if err != nil {
		t.Fatalf("bind deployment target: %v", err)
	}
	if rows != 1 {
		t.Fatalf("bound rows = %d, want 1", rows)
	}
	rows, err = queries.BindMediaDeploymentTarget(ctx, target)
	if err != nil {
		t.Fatalf("re-bind deployment target: %v", err)
	}
	if rows != 0 {
		t.Fatalf("re-bound rows = %d, want 0", rows)
	}
	drifted := dbq.BindMediaDeploymentTargetParams{
		ProtocolVersion:       pgtype.Int2{Int16: 2, Valid: true},
		DeploymentID:          pgtype.Text{String: "22222222-2222-4222-8222-222222222222", Valid: true},
		SourceBackendIdentity: pgtype.Text{String: "sha256:v1:" + strings.Repeat("b", 64), Valid: true},
		MediaBackendIdentity:  pgtype.Text{String: "sha256:v1:" + strings.Repeat("c", 64), Valid: true},
	}
	rows, err = queries.BindMediaDeploymentTarget(ctx, drifted)
	if err != nil {
		t.Fatalf("attempt drifted deployment target bind: %v", err)
	}
	if rows != 0 {
		t.Fatalf("drifted target changed %d rows, want 0", rows)
	}

	bound, err := queries.GetMediaDeploymentTarget(ctx)
	if err != nil {
		t.Fatalf("read deployment target: %v", err)
	}
	if bound.ProtocolVersion != 2 || bound.DeploymentID != target.DeploymentID.String ||
		bound.SourceBackendIdentity != target.SourceBackendIdentity.String ||
		bound.MediaBackendIdentity != target.MediaBackendIdentity.String {
		t.Fatalf("unexpected persisted deployment target: %+v", bound)
	}
}
