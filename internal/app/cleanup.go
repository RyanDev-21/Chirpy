package app

import (
	"context"
	"log/slog"

	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/platform/revoke"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

// cacheCleaner removes cached state about a deleted account: the user's own
// entries, their friends' relationship snapshots, and their groups' member
// lists, then drops the live connection.
type cacheCleaner struct {
	friends friends.Store
	groups  groups.Store
	users   users.Store
	hub     *ws.Hub
	revoked *revoke.Store
	logger  *slog.Logger
}

// sessionEnder makes "revoke all refresh tokens" also end the user's
// access tokens, which otherwise stay valid until they expire.
type sessionEnder struct {
	users.UserRepo
	revoked *revoke.Store
	hub     *ws.Hub
	// devices: phones stop getting notifications until they sign in again
	devices interface {
		ForgetDevices(ctx context.Context, userID uuid.UUID) error
	}
}

func (s *sessionEnder) RevokeAllRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	if err := s.UserRepo.RevokeAllRefreshTokens(ctx, userID); err != nil {
		return err
	}
	if err := s.revoked.RevokeAll(ctx, userID); err != nil {
		return err
	}
	s.hub.Disconnect(userID) // the socket was opened with an old token
	if s.devices != nil {
		if err := s.devices.ForgetDevices(ctx, userID); err != nil {
			return err
		}
	}
	return nil
}

func (c *cacheCleaner) AccountDeleted(ctx context.Context, userID uuid.UUID, friendIDs, groupIDs []uuid.UUID) {
	// the account's access tokens must stop working now, not in 15 minutes
	if err := c.revoked.RevokeAll(ctx, userID); err != nil {
		c.logger.ErrorContext(ctx, "failed to revoke tokens of deleted account", "error", err)
	}
	ids := make([]string, 0, len(friendIDs)+1)
	ids = append(ids, userID.String())
	for _, f := range friendIDs {
		ids = append(ids, f.String())
	}
	if err := c.friends.Invalidate(ctx, ids...); err != nil {
		c.logger.ErrorContext(ctx, "failed to invalidate friends cache", "error", err)
	}
	gids := make([]string, len(groupIDs))
	for i, g := range groupIDs {
		gids[i] = g.String()
	}
	if len(gids) > 0 {
		if err := c.groups.Invalidate(ctx, gids...); err != nil {
			c.logger.ErrorContext(ctx, "failed to invalidate group cache", "error", err)
		}
	}
	if err := c.users.Forget(ctx, userID); err != nil {
		c.logger.ErrorContext(ctx, "failed to forget user cache", "error", err)
	}
	c.hub.Disconnect(userID)
}

// pushNames resolves the names shown in notifications.
type pushNames struct {
	users  users.UserService
	groups groups.GroupService
}

// GetName is the sender as recipients know them: the display name.
func (p pushNames) GetName(ctx context.Context, id uuid.UUID) (string, error) {
	return p.users.DisplayName(ctx, id)
}

func (p pushNames) GroupName(ctx context.Context, id uuid.UUID) (string, error) {
	return p.groups.GroupName(ctx, id)
}
