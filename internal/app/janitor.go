package app

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	janitorEvery = time.Hour
	// revoked refresh tokens are kept this long so a "refresh rejected"
	// log line can still be matched to a row when investigating
	revokedKeep = 24 * time.Hour
	// the call log (who, when, how long) is kept 180 days
	callLogKeep = 180 * 24 * time.Hour
)

// runJanitor deletes rows nothing will read again: expired or revoked
// refresh tokens (every refresh revokes one, so they pile up) and expired
// email codes. Access tokens are stateless JWTs and are never stored.
// Deletes are idempotent, so several instances can run this at once.
func (a *App) runJanitor(ctx context.Context) {
	wait := time.Minute // first pass shortly after start
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := a.Cleanup(ctx); err != nil && ctx.Err() == nil {
			a.logger.ErrorContext(ctx, "cleanup failed", "error", err)
		}
		wait = janitorEvery
	}
}

// Cleanup runs one janitor pass.
func (a *App) Cleanup(ctx context.Context) error {
	q := database.New(a.db)
	now := time.Now().UTC()
	tokens, err := q.DeleteStaleRefreshTokens(ctx, database.DeleteStaleRefreshTokensParams{
		Now:           ts(now),
		RevokedBefore: ts(now.Add(-revokedKeep)),
	})
	if err != nil {
		return err
	}
	codes, err := q.DeleteExpiredEmailCodes(ctx, ts(now))
	if err != nil {
		return err
	}
	if n, err := q.PurgeOldCalls(ctx, ts(now.Add(-callLogKeep))); err != nil {
		a.logger.WarnContext(ctx, "failed to purge old calls", "error", err)
	} else if n > 0 {
		a.logger.InfoContext(ctx, "old call log entries deleted", "calls", n)
	}
	if n, err := a.admin.PurgeOldReports(ctx); err != nil {
		a.logger.WarnContext(ctx, "failed to purge old reports", "error", err)
	} else if n > 0 {
		a.logger.InfoContext(ctx, "old closed reports deleted", "reports", n)
	}
	if lifted, err := a.admin.LiftExpired(ctx); err != nil {
		a.logger.WarnContext(ctx, "failed to lift expired bans", "error", err)
	} else if lifted > 0 {
		a.logger.InfoContext(ctx, "temporary bans lifted", "users", lifted)
	}
	files, err := a.media.Cleanup(ctx)
	if err != nil {
		return err
	}
	if files > 0 {
		a.logger.InfoContext(ctx, "media cleanup", "files_deleted", files)
	}
	// storage gauges, read here so monitoring never wakes the database
	var neon int64
	if err := a.db.QueryRow(ctx, "SELECT pg_database_size(current_database())").Scan(&neon); err != nil {
		a.logger.WarnContext(ctx, "failed to read database size", "error", err)
	}
	r2, err := q.MediaStoredBytes(ctx)
	if err != nil {
		a.logger.WarnContext(ctx, "failed to read media size", "error", err)
	}
	telemetry.StorageSizes(neon, r2)
	if tokens > 0 || codes > 0 {
		a.logger.InfoContext(ctx, "cleanup", "refresh_tokens", tokens, "email_codes", codes)
	}
	return nil
}

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t, Valid: true} }
