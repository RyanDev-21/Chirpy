package app

import (
	"context"
	"log/slog"

	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/groups"
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
	logger  *slog.Logger
}

func (c *cacheCleaner) AccountDeleted(ctx context.Context, userID uuid.UUID, friendIDs, groupIDs []uuid.UUID) {
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
