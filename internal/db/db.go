package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Pool is a convenience alias for pgxpool.Pool.
type Pool = pgxpool.Pool

// Connect creates a connection pool to PostgreSQL.
func Connect(ctx context.Context, databaseURL string) (*Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping pg: %w", err)
	}

	slog.Info("connected to PostgreSQL")

	return pool, nil
}

// Migrate runs all pending goose migrations.
// It uses database/sql via pgx/stdlib under the hood because goose
// does not natively support pgxpool.
// The caller must provide the embedded migrations FS (from the module root).
func Migrate(ctx context.Context, databaseURL string, migrationsFS embed.FS) error {
	goose.SetBaseFS(migrationsFS)

	sqlDB, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("goose open db: %w", err)
	}
	defer sqlDB.Close()

	if err := goose.RunContext(ctx, "up", sqlDB, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	slog.Info("database migrations applied")

	return nil
}
