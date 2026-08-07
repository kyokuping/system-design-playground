package shortener

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

const migrationLockID int64 = 7_616_498_672_576

func migratePostgres(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		if err := applyMigration(ctx, pool, version, string(contents)); err != nil {
			return fmt.Errorf("apply migration %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func migrationVersion(filename string) (int64, error) {
	prefix, _, found := strings.Cut(filename, "_")
	if !found {
		return 0, fmt.Errorf("migration %q has no numeric prefix", filename)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %q has an invalid version", filename)
	}
	return version, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version int64, contents string) error {
	transaction, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return err
	}
	var applied bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return transaction.Commit(ctx)
	}
	if _, err := transaction.Exec(ctx, contents, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
