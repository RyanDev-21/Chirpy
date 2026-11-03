package groups

import (
	"context"
	"errors"

	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"github.com/jackc/pgx/v5/pgconn"
)

// workers persist group changes to postgres. Membership jobs can arrive
// before the create job has committed; the foreign-key error is retried.

func (s *groupService) HandleCreate(ctx context.Context, j JobCreate) error {
	err := s.repo.CreateWithLeader(ctx, j)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// another group won the name in postgres
		return queue.Permanent(err)
	}
	return err
}

func (s *groupService) HandleAddMember(ctx context.Context, j JobMember) error {
	return s.repo.AddMember(ctx, j.GroupID, j.UserID)
}

var errMemberNotPersisted = errors.New("membership not in postgres yet")

// a remove that deletes nothing usually means the add job has not run yet,
// so it is retried; this keeps join-then-leave ordered.
func (s *groupService) HandleRemoveMember(ctx context.Context, j JobMember) error {
	deleted, err := s.repo.RemoveMember(ctx, j.GroupID, j.UserID)
	if err != nil {
		return err
	}
	if !deleted {
		return errMemberNotPersisted
	}
	return nil
}
