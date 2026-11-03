// Package migrate applies the embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"RyanDev-21.com/Chirpy/sql/schema"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Up applies every pending migration. A Postgres advisory lock makes it
// safe when several instances start at the same time.
func Up(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, schema.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		logger.InfoContext(ctx, "migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "database schema up to date", "version", version)
	return nil
}
