// Package admin is "god mode": metadata about users and groups, and bans.
// Admins never see message content or media. Admin rights are granted only
// with the cmd/admin CLI, and every admin action is written to an audit log.
package admin

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("user not found")
	ErrBanSelf      = errors.New("you can't ban yourself")
	ErrBanAdmin     = errors.New("admins can't be banned (remove admin rights with the CLI first)")
	ErrBadBan       = errors.New("a reason (1-500 characters) is required; until must be in the future")
	ErrNotBanned    = errors.New("user is not banned")
	ErrUnknownEmail = errors.New("no account with that email")
)

// SessionEnder ends all of a user's sessions: refresh and access tokens,
// sockets and phones (the app's sessionEnder).
type SessionEnder interface {
	RevokeAllRefreshTokens(ctx context.Context, userID uuid.UUID) error
}

type Service struct {
	pool    *pgxpool.Pool
	q       *database.Queries
	ender   SessionEnder
	changed func(ctx context.Context, groupID uuid.UUID) // drops cached group state
	onBan   []func(ctx context.Context, userID uuid.UUID)
	logger  *slog.Logger
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, ender SessionEnder, groupChanged func(context.Context, uuid.UUID), logger *slog.Logger) *Service {
	return &Service{pool: pool, q: database.New(pool), ender: ender, changed: groupChanged, logger: logger,
		now: func() time.Time { return time.Now().UTC() }}
}

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t, Valid: true} }

func optUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil} }

func optTime(t pgtype.Timestamp) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// Log writes an audit entry; admin is uuid.Nil for the CLI or the system.
func (s *Service) Log(ctx context.Context, admin uuid.UUID, action string, user, group uuid.UUID, detail string) {
	id, _ := uuid.NewV7()
	if err := s.q.LogAdminAction(ctx, database.LogAdminActionParams{
		ID: id, AdminID: optUUID(admin), Action: action, TargetUser: optUUID(user), TargetGroup: optUUID(group),
		Detail: detail, At: ts(s.now()),
	}); err != nil {
		s.logger.ErrorContext(ctx, "failed to write admin audit log", "action", action, "error", err)
	}
}

// --- read-only views (metadata only) ---

type Overview struct {
	Users       int64 `json:"users"`
	Verified    int64 `json:"verified"`
	Signups24h  int64 `json:"signups_24h"`
	Signups7d   int64 `json:"signups_7d"`
	Active7d    int64 `json:"active_7d"`
	Banned      int64 `json:"banned"`
	Messages24h int64 `json:"messages_24h"`
	Groups      int64 `json:"groups"`
	MediaBytes  int64 `json:"media_bytes"`
	OpenReports int64 `json:"open_reports"`
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := s.now()
	o, err := s.q.AdminOverview(ctx, database.AdminOverviewParams{DayAgo: ts(now.Add(-24 * time.Hour)), WeekAgo: ts(now.Add(-7 * 24 * time.Hour))})
	if err != nil {
		return Overview{}, err
	}
	open, err := s.q.CountOpenReports(ctx)
	return Overview{Users: o.Users, Verified: o.Verified, Signups24h: o.Signups24h, Signups7d: o.Signups7d, Active7d: o.Active7d,
		Banned: o.Banned, Messages24h: o.Messages24h, Groups: o.Groups, MediaBytes: o.MediaBytes, OpenReports: open}, err
}

type User struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email"`
	Verified    bool       `json:"email_verified"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	IsAdmin     bool       `json:"is_admin"`
	BannedAt    *time.Time `json:"banned_at"`
	BannedUntil *time.Time `json:"banned_until"` // null with banned_at set: permanent
	BanReason   string     `json:"ban_reason,omitempty"`
	// Stats are only filled in on a single user's page.
	Stats *UserStats `json:"stats,omitempty"`
}

type UserStats struct {
	MessagesSent int64 `json:"messages_sent"`
	Friends      int64 `json:"friends"`
	Groups       int64 `json:"groups"`
	Devices      int64 `json:"devices"`
}

func toUser(id uuid.UUID, name, display, email string, verified bool, created time.Time, seen pgtype.Timestamp, admin bool,
	bannedAt, bannedUntil pgtype.Timestamp, reason pgtype.Text) User {
	if display == "" {
		display = name
	}
	return User{ID: id, Name: name, DisplayName: display, Email: email, Verified: verified, CreatedAt: created.UTC(), LastSeenAt: optTime(seen),
		IsAdmin: admin, BannedAt: optTime(bannedAt), BannedUntil: optTime(bannedUntil), BanReason: reason.String}
}

func page(limit, offset int) (int32, int32) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return int32(limit), int32(max(offset, 0))
}

func (s *Service) Users(ctx context.Context, q string, limit, offset int) ([]User, error) {
	lim, off := page(limit, offset)
	rows, err := s.q.AdminSearchUsers(ctx, database.AdminSearchUsersParams{Q: strings.TrimSpace(q), Lim: lim, Off: off})
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(rows))
	for _, r := range rows {
		out = append(out, toUser(r.ID, r.Name, r.DisplayName, r.Email, r.Verified, r.CreatedAt, r.LastSeenAt, r.IsAdmin, r.BannedAt, r.BannedUntil, r.BanReason))
	}
	return out, nil
}

func (s *Service) User(ctx context.Context, id uuid.UUID) (User, error) {
	r, err := s.q.AdminGetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u := toUser(r.ID, r.Name, r.DisplayName, r.Email, r.Verified, r.CreatedAt, r.LastSeenAt, r.IsAdmin, r.BannedAt, r.BannedUntil, r.BanReason)
	st, err := s.q.AdminUserStats(ctx, id)
	if err != nil {
		return User{}, err
	}
	u.Stats = &UserStats{MessagesSent: st.MessagesSent, Friends: st.Friends, Groups: st.Groups, Devices: st.Devices}
	return u, nil
}

type Group struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Members    int16     `json:"member_count"`
	Leader     string    `json:"leader"`
	CreatedAt  time.Time `json:"created_at"`
	Messages7d int64     `json:"messages_7d"`
}

func (s *Service) Groups(ctx context.Context, q string, limit, offset int) ([]Group, error) {
	lim, off := page(limit, offset)
	rows, err := s.q.AdminSearchGroups(ctx, database.AdminSearchGroupsParams{Q: strings.TrimSpace(q), Lim: lim, Off: off,
		WeekAgo: ts(s.now().Add(-7 * 24 * time.Hour))})
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(rows))
	for _, r := range rows {
		out = append(out, Group{ID: r.ID, Name: r.Name, Members: r.CurrentMember, Leader: r.Leader, CreatedAt: r.CreatedAt.Time.UTC(), Messages7d: r.Messages7d})
	}
	return out, nil
}

type Action struct {
	ID          uuid.UUID  `json:"id"`
	Admin       string     `json:"admin"`
	Action      string     `json:"action"`
	TargetUser  *uuid.UUID `json:"target_user,omitempty"`
	TargetGroup *uuid.UUID `json:"target_group,omitempty"`
	Detail      string     `json:"detail,omitempty"`
	At          time.Time  `json:"at"`
}

func (s *Service) Audit(ctx context.Context, limit int) ([]Action, error) {
	lim, _ := page(limit, 0)
	rows, err := s.q.ListAdminActions(ctx, lim)
	if err != nil {
		return nil, err
	}
	out := make([]Action, 0, len(rows))
	for _, r := range rows {
		a := Action{ID: r.ID, Admin: r.Admin, Action: r.Action, Detail: r.Detail, At: r.At.Time.UTC()}
		if r.TargetUser.Valid {
			u := uuid.UUID(r.TargetUser.Bytes)
			a.TargetUser = &u
		}
		if r.TargetGroup.Valid {
			g := uuid.UUID(r.TargetGroup.Bytes)
			a.TargetGroup = &g
		}
		out = append(out, a)
	}
	return out, nil
}

// --- bans ---

// Ban suspends target: no new sessions, current ones end, hidden from
// search/discovery/profiles, removed from groups with other members (a
// leader hands over to the earliest member), past messages hidden. until
// nil is permanent.
func (s *Service) Ban(ctx context.Context, admin, target uuid.UUID, reason string, until *time.Time) error {
	reason = strings.TrimSpace(reason)
	if target == admin {
		return ErrBanSelf
	}
	if n := utf8.RuneCountInString(reason); n == 0 || n > 500 || (until != nil && !until.After(s.now())) {
		return ErrBadBan
	}
	untilTS := pgtype.Timestamp{}
	if until != nil {
		untilTS = ts(until.UTC())
	}
	n, err := s.q.BanUser(ctx, database.BanUserParams{ID: target, Now: ts(s.now()), Until: untilTS, Reason: pgtype.Text{String: reason, Valid: true}})
	if err != nil {
		return err
	}
	if n == 0 {
		b, err := s.q.UserBan(ctx, target)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case b.IsAdmin:
			return ErrBanAdmin
		}
		return ErrNotFound
	}
	if err := s.ender.RevokeAllRefreshTokens(ctx, target); err != nil {
		return err
	}
	if err := s.leaveGroups(ctx, target); err != nil {
		return err
	}
	detail := "permanent: " + reason
	if until != nil {
		detail = "until " + until.UTC().Format(time.RFC3339) + ": " + reason
	}
	s.Log(ctx, admin, "ban", target, uuid.Nil, detail)
	s.bansChanged(ctx, target)
	return nil
}

// leaveGroups removes a banned user from every group that has other
// members. Groups where they're alone are left as they are.
func (s *Service) leaveGroups(ctx context.Context, user uuid.UUID) error {
	ms, err := s.q.GroupMembershipsOf(ctx, user)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.CurrentMember <= 1 {
			continue
		}
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			if m.Role == "admin" {
				next, err := q.EarliestOtherMember(ctx, database.EarliestOtherMemberParams{GroupID: m.GroupID, MemberID: user})
				if errors.Is(err, pgx.ErrNoRows) {
					return nil // alone after all
				}
				if err != nil {
					return err
				}
				if err := q.PromoteToLeader(ctx, database.PromoteToLeaderParams{GroupID: m.GroupID, MemberID: next}); err != nil {
					return err
				}
			}
			if err := q.DeleteMemFromGroup(ctx, database.DeleteMemFromGroupParams{GroupID: m.GroupID, MemberID: user}); err != nil {
				return err
			}
			return q.RecountGroupMembers(ctx, m.GroupID)
		})
		if err != nil {
			return err
		}
		if s.changed != nil {
			s.changed(ctx, m.GroupID)
		}
		s.Log(ctx, uuid.Nil, "ban_removed_from_group", user, m.GroupID, "")
	}
	return nil
}

func (s *Service) Unban(ctx context.Context, admin, target uuid.UUID) error {
	n, err := s.q.UnbanUser(ctx, target)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotBanned
	}
	s.Log(ctx, admin, "unban", target, uuid.Nil, "")
	s.bansChanged(ctx, target)
	return nil
}

// LiftExpired ends temporary bans whose time is up (hourly cleanup).
func (s *Service) LiftExpired(ctx context.Context) (int, error) {
	ids, err := s.q.ExpiredBans(ctx, ts(s.now()))
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := s.q.UnbanUser(ctx, id); err != nil {
			return 0, err
		}
		s.Log(ctx, uuid.Nil, "ban_expired", id, uuid.Nil, "")
		s.bansChanged(ctx, id)
	}
	return len(ids), nil
}

// IsActiveAdmin is checked on every admin request (rights can be revoked).
func (s *Service) IsActiveAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	b, err := s.q.UserBan(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && b.IsAdmin && !b.BannedAt.Valid, err
}

// --- CLI ---

// SetAdmin grants or revokes admin rights by email (cmd/admin only).
func (s *Service) SetAdmin(ctx context.Context, email string, on bool) (string, error) {
	r, err := s.q.SetAdminByEmail(ctx, database.SetAdminByEmailParams{Email: strings.TrimSpace(email), IsAdmin: on})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnknownEmail
	}
	if err != nil {
		return "", err
	}
	action := "grant_admin"
	if !on {
		action = "revoke_admin"
		// an admin page already open must stop working now
		if err := s.ender.RevokeAllRefreshTokens(ctx, r.ID); err != nil {
			return "", err
		}
	}
	s.Log(ctx, uuid.Nil, action, r.ID, uuid.Nil, "via cmd/admin")
	return r.Name, nil
}

type Admin struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
}

func (s *Service) Admins(ctx context.Context) ([]Admin, error) {
	rows, err := s.q.ListAdmins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Admin, len(rows))
	for i, r := range rows {
		out[i] = Admin{ID: r.ID, Name: r.Name, Email: r.Email}
	}
	return out, nil
}

// --- reports ---

var (
	ErrReportNotFound = errors.New("report not found")
	ErrBadResolution  = errors.New(`status must be "resolved" or "dismissed"; note at most 1000 characters`)
	ErrReportClosed   = errors.New("report is already closed")
)

type ReportSummary struct {
	ID             uuid.UUID `json:"id"`
	Reason         string    `json:"reason"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	ReportedID     uuid.UUID `json:"reported_id"`
	ReportedName   string    `json:"reported_name"`
	ReporterName   string    `json:"reporter_name"`
	Evidence       int       `json:"evidence"`        // attached messages
	ReportsAgainst int       `json:"reports_against"` // all reports about this user
}

// Evidence is a message the reporter chose to attach, as it was then.
type Evidence struct {
	MessageID uuid.UUID  `json:"message_id"`
	ChatType  string     `json:"chat_type"`
	GroupID   *uuid.UUID `json:"group_id,omitempty"`
	SenderID  *uuid.UUID `json:"sender_id"`
	Content   string     `json:"content"`
	SentAt    time.Time  `json:"sent_at"`
}

type ReportDetail struct {
	ReportSummary
	ReporterID *uuid.UUID `json:"reporter_id"`
	Note       string     `json:"note"`
	Resolution string     `json:"resolution"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Messages   []Evidence `json:"messages"`
}

func optID(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

func (s *Service) Reports(ctx context.Context, status string, limit, offset int) ([]ReportSummary, error) {
	lim, off := page(limit, offset)
	rows, err := s.q.ListReports(ctx, database.ListReportsParams{Status: status, Lim: lim, Off: off})
	if err != nil {
		return nil, err
	}
	out := make([]ReportSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReportSummary{ID: r.ID, Reason: r.Reason, Status: r.Status, CreatedAt: r.CreatedAt.Time.UTC(),
			ReportedID: r.ReportedID, ReportedName: r.ReportedName, ReporterName: r.ReporterName,
			Evidence: int(r.Evidence), ReportsAgainst: int(r.ReportsAgainst)})
	}
	return out, nil
}

// Report shows one report with its evidence (logged as a view).
func (s *Service) Report(ctx context.Context, admin, id uuid.UUID) (ReportDetail, error) {
	r, err := s.q.GetReport(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportDetail{}, ErrReportNotFound
	}
	if err != nil {
		return ReportDetail{}, err
	}
	msgs, err := s.q.ReportMessages(ctx, id)
	if err != nil {
		return ReportDetail{}, err
	}
	d := ReportDetail{
		ReportSummary: ReportSummary{ID: r.ID, Reason: r.Reason, Status: r.Status, CreatedAt: r.CreatedAt.Time.UTC(),
			ReportedID: r.ReportedID, ReportedName: r.ReportedName, ReporterName: r.ReporterName, Evidence: len(msgs)},
		ReporterID: optID(r.ReporterID), Note: r.Note, Resolution: r.Resolution, ResolvedAt: optTime(r.ResolvedAt),
		Messages: make([]Evidence, 0, len(msgs)),
	}
	for _, m := range msgs {
		d.Messages = append(d.Messages, Evidence{MessageID: m.MessageID, ChatType: m.ChatType, GroupID: optID(m.GroupID),
			SenderID: optID(m.SenderID), Content: m.Content, SentAt: m.SentAt.Time.UTC()})
	}
	s.Log(ctx, admin, "view_report", r.ReportedID, uuid.Nil, "report "+id.String())
	return d, nil
}

// Resolve closes an open report as "resolved" (action taken) or
// "dismissed" (no action). Banning is a separate step.
func (s *Service) Resolve(ctx context.Context, admin, id uuid.UUID, status, note string) error {
	note = strings.TrimSpace(note)
	if (status != "resolved" && status != "dismissed") || utf8.RuneCountInString(note) > 1000 {
		return ErrBadResolution
	}
	n, err := s.q.ResolveReport(ctx, database.ResolveReportParams{ID: id, Status: status, AdminID: optUUID(admin), Now: ts(s.now()), Resolution: note})
	if err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.q.GetReport(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return ErrReportNotFound
		}
		return ErrReportClosed
	}
	s.Log(ctx, admin, "report_"+status, uuid.Nil, uuid.Nil, "report "+id.String()+": "+note)
	return nil
}

// reportRetention: closed reports and their copied evidence are deleted
// after this (privacy: moderation records aren't kept forever).
const reportRetention = 180 * 24 * time.Hour

// PurgeOldReports runs in the hourly cleanup.
func (s *Service) PurgeOldReports(ctx context.Context) (int64, error) {
	return s.q.DeleteOldClosedReports(ctx, ts(s.now().Add(-reportRetention)))
}

// OnBanChange registers code to run after a ban starts or ends (the chat
// service refreshes which senders it hides).
func (s *Service) OnBanChange(fn func(ctx context.Context, userID uuid.UUID)) {
	s.onBan = append(s.onBan, fn)
}

func (s *Service) bansChanged(ctx context.Context, id uuid.UUID) {
	for _, fn := range s.onBan {
		fn(ctx, id)
	}
}
