// Package reports lets users report another user to the admins, optionally
// attaching messages they choose as evidence. Evidence must come from a
// chat the reporter is in and is copied at report time; admins see only
// that, never anything else from the chat.
package reports

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reasons a user can pick.
var Reasons = []string{"spam", "harassment", "hate", "violence", "sexual", "impersonation", "scam", "other"}

const (
	maxNote     = 1000
	maxEvidence = 20
)

var (
	ErrBadReport   = errors.New("reason must be one of spam, harassment, hate, violence, sexual, impersonation, scam, other; note at most 1000 characters; at most 20 messages")
	ErrSelf        = errors.New("you can't report yourself")
	ErrUnknownUser = errors.New("user not found")
	ErrBadEvidence = errors.New("each attached message must be from your chat with this user or from a group you're in")
)

type Service struct {
	pool   *pgxpool.Pool
	q      *database.Queries
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: database.New(pool), logger: logger}
}

// Request is POST /api/reports.
type Request struct {
	UserID   uuid.UUID   `json:"user_id"`
	Reason   string      `json:"reason"`
	Note     string      `json:"note"`
	Messages []uuid.UUID `json:"messages"`
}

func validReason(r string) bool {
	for _, x := range Reasons {
		if x == r {
			return true
		}
	}
	return false
}

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t, Valid: true} }

// Report files (or updates the open) report from reporter about req.UserID.
func (s *Service) Report(ctx context.Context, reporter uuid.UUID, req Request) (uuid.UUID, error) {
	req.Note = strings.TrimSpace(req.Note)
	if !validReason(req.Reason) || utf8.RuneCountInString(req.Note) > maxNote || len(req.Messages) > maxEvidence {
		return uuid.Nil, ErrBadReport
	}
	if req.UserID == reporter {
		return uuid.Nil, ErrSelf
	}
	ids := dedupe(req.Messages)

	// evidence: only messages the reporter can see, from a chat they're in
	type msg struct {
		id, sender, group uuid.UUID
		kind, content     string
		at                time.Time
	}
	var evidence []msg
	if len(ids) > 0 {
		priv, err := s.q.PrivateEvidence(ctx, database.PrivateEvidenceParams{Ids: ids, Reporter: reporter, Reported: req.UserID})
		if err != nil {
			return uuid.Nil, err
		}
		for _, m := range priv {
			evidence = append(evidence, msg{id: m.ID, sender: uuid.UUID(m.FromID.Bytes), kind: "private", content: m.Content.String, at: m.CreatedAt.Time})
		}
		grp, err := s.q.GroupEvidence(ctx, database.GroupEvidenceParams{Ids: ids, Reporter: reporter})
		if err != nil {
			return uuid.Nil, err
		}
		for _, m := range grp {
			evidence = append(evidence, msg{id: m.ID, sender: uuid.UUID(m.FromID.Bytes), group: uuid.UUID(m.GroupID.Bytes), kind: "public", content: m.Content.String, at: m.CreatedAt.Time})
		}
		if len(evidence) != len(ids) {
			return uuid.Nil, ErrBadEvidence
		}
	}

	var reportID uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		newID, _ := uuid.NewV7()
		var err error
		reportID, err = q.UpsertOpenReport(ctx, database.UpsertOpenReportParams{
			ID: newID, ReporterID: pgtype.UUID{Bytes: reporter, Valid: true}, ReportedID: req.UserID,
			Reason: req.Reason, Note: req.Note, Now: ts(time.Now().UTC()),
		})
		if err != nil {
			return err
		}
		for _, m := range evidence {
			if err := q.AddReportMessage(ctx, database.AddReportMessageParams{
				ReportID: reportID, MessageID: m.id, ChatType: m.kind,
				GroupID: pgtype.UUID{Bytes: m.group, Valid: m.group != uuid.Nil}, SenderID: pgtype.UUID{Bytes: m.sender, Valid: m.sender != uuid.Nil},
				Content: m.content, SentAt: ts(m.at),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // reported user doesn't exist
		return uuid.Nil, ErrUnknownUser
	}
	if err != nil {
		return uuid.Nil, err
	}
	telemetry.Report(ctx, req.Reason)
	return reportID, nil
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Create handles POST /api/reports {"user_id","reason","note"?,"messages"?}.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req Request
	if err := httpx.Decode(w, r, &req); err != nil || req.UserID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadReport.Error())
		return
	}
	id, err := h.svc.Report(r.Context(), me, req)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusCreated, map[string]uuid.UUID{"report_id": id})
	case errors.Is(err, ErrBadReport), errors.Is(err, ErrSelf), errors.Is(err, ErrBadEvidence):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrUnknownUser):
		httpx.Error(w, http.StatusNotFound, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}
