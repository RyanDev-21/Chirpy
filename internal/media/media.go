// Package media handles photos and videos in messages. Files live in
// Cloudflare R2 (free tier: 10 GB); phones upload and download them
// directly with short-lived signed links. To stay free, files are deleted
// 24 h after every recipient downloaded them, after 7 days at most, and by
// least-recently-used cleanup when storage nears 10 GB.
package media

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	MaxImageBytes  = 5 << 20
	MaxVideoBytes  = 25 << 20
	DailyUserBytes = 200 << 20
	MaxPerMessage  = 10

	// R2's free tier is 10 GB: new uploads stop at 9.5 GB, and the cleanup
	// deletes least-recently-downloaded files above 9 GB down to 8 GB.
	uploadCapBytes = 9_500 << 20
	lruHighBytes   = 9 << 30
	lruLowBytes    = 8 << 30

	maxLifetime  = 7 * 24 * time.Hour
	keepAfterAll = 24 * time.Hour // after every recipient downloaded it
	pendingTTL   = time.Hour      // an upload link nobody attached
	uploadTTL    = 10 * time.Minute
	downloadTTL  = 5 * time.Minute
	// groups bigger than this skip per-member delivery tracking (7-day rule only)
	trackGroupsUpTo = 50
)

var (
	ErrDisabled      = errors.New("media is not configured on this server")
	ErrBadMedia      = errors.New("kind must be image (JPEG, PNG, WebP, HEIC; max 5 MB) or video (MP4, MOV, WebM; max 25 MB) with its size in bytes")
	ErrDailyQuota    = errors.New("daily upload limit reached (200 MB per day)")
	ErrStorageFull   = errors.New("media storage is full right now; try again later")
	ErrNotFound      = errors.New("media not found")
	ErrExpired       = errors.New("this media has expired")
	ErrBadAttachment = errors.New("media must be your own uploads (at most 10), each uploaded and not attached to another message")
)

var allowedTypes = map[string]map[string]bool{
	"image": {"image/jpeg": true, "image/png": true, "image/webp": true, "image/heic": true},
	"video": {"video/mp4": true, "video/quicktime": true, "video/webm": true},
}

// Attachment is media as shown on a message.
type Attachment struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Expired: removed from storage; the phone shows its own copy, if any.
	Expired bool `json:"expired"`
}

// Members lists a group's members (groups.GroupService).
type Members interface {
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
}

type Service struct {
	q       *database.Queries
	store   Storage // nil: media disabled
	members Members
	logger  *slog.Logger
	now     func() time.Time
}

func NewService(q *database.Queries, store Storage, members Members, logger *slog.Logger) *Service {
	return &Service{q: q, store: store, members: members, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}

func key(id uuid.UUID) string { return "m/" + id.String() }

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t, Valid: true} }

// Upload is the answer to POST /api/media.
type Upload struct {
	ID        uuid.UUID         `json:"media_id"`
	URL       string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresIn int               `json:"expires_in"`
}

// NewUpload checks limits and returns a link to PUT the file to.
func (s *Service) NewUpload(ctx context.Context, owner uuid.UUID, kind, contentType string, size int64) (Upload, error) {
	if s.store == nil {
		return Upload{}, ErrDisabled
	}
	limit := int64(MaxImageBytes)
	if kind == "video" {
		limit = MaxVideoBytes
	}
	if !allowedTypes[kind][contentType] || size <= 0 || size > limit {
		return Upload{}, ErrBadMedia
	}
	now := s.now()
	used, err := s.q.MediaUploadedSince(ctx, database.MediaUploadedSinceParams{OwnerID: owner, Since: ts(now.Add(-24 * time.Hour))})
	if err != nil {
		return Upload{}, err
	}
	if used+size > DailyUserBytes {
		return Upload{}, ErrDailyQuota
	}
	stored, err := s.q.MediaStoredBytes(ctx)
	if err != nil {
		return Upload{}, err
	}
	if stored+size > uploadCapBytes {
		return Upload{}, ErrStorageFull
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Upload{}, err
	}
	if err := s.q.CreateMedia(ctx, database.CreateMediaParams{
		ID: id, OwnerID: owner, Kind: kind, ContentType: contentType, Size: size,
		Now: ts(now), ExpiresAt: ts(now.Add(maxLifetime)),
	}); err != nil {
		return Upload{}, err
	}
	u, err := s.store.PresignPut(key(id), contentType, size, uploadTTL)
	if err != nil {
		return Upload{}, err
	}
	return Upload{ID: id, URL: u, Method: "PUT", ExpiresIn: int(uploadTTL.Seconds()),
		Headers: map[string]string{"Content-Type": contentType}}, nil
}

// Attach links uploads to a new message. Each must be the sender's own,
// uploaded (present in storage with the declared size) and not yet used.
func (s *Service) Attach(ctx context.Context, owner uuid.UUID, ids []uuid.UUID, chatType string, chatID, msgID uuid.UUID) ([]Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s.store == nil {
		return nil, ErrDisabled
	}
	if len(ids) > MaxPerMessage {
		return nil, ErrBadAttachment
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, ErrBadAttachment
		}
		seen[id] = true
	}
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		m, err := s.q.GetMedia(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (m.OwnerID != owner || m.Status != "pending" || m.DeletedAt.Valid)) {
			return nil, ErrBadAttachment
		}
		if err != nil {
			return nil, err
		}
		size, err := s.store.Size(ctx, key(id))
		if errors.Is(err, ErrNoObject) || (err == nil && size != m.Size) {
			return nil, ErrBadAttachment
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Attachment{ID: id, Kind: m.Kind, ContentType: m.ContentType, Size: m.Size, ExpiresAt: m.ExpiresAt.Time.UTC()})
	}
	for _, id := range ids {
		n, err := s.q.AttachMedia(ctx, database.AttachMediaParams{
			ID: id, OwnerID: owner, ChatType: pgtype.Text{String: chatType, Valid: true},
			ChatID: pgtype.UUID{Bytes: chatID, Valid: true}, MessageID: pgtype.UUID{Bytes: msgID, Valid: true},
		})
		if err != nil {
			return nil, err
		}
		if n == 0 { // attached by a concurrent message
			return nil, ErrBadAttachment
		}
	}
	return out, nil
}

// ForMessages returns the media of a page of messages.
func (s *Service) ForMessages(ctx context.Context, msgIDs []uuid.UUID) (map[uuid.UUID][]Attachment, error) {
	out := map[uuid.UUID][]Attachment{}
	if len(msgIDs) == 0 {
		return out, nil
	}
	rows, err := s.q.MediaForMessages(ctx, msgIDs)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		mid := uuid.UUID(r.MessageID.Bytes)
		out[mid] = append(out[mid], Attachment{ID: r.ID, Kind: r.Kind, ContentType: r.ContentType, Size: r.Size,
			ExpiresAt: r.ExpiresAt.Time.UTC(), Expired: r.DeletedAt.Valid})
	}
	return out, nil
}

// ExpireMessage schedules a deleted message's media for removal.
func (s *Service) ExpireMessage(ctx context.Context, msgID uuid.UUID) error {
	return s.q.ExpireMediaOfMessage(ctx, database.ExpireMediaOfMessageParams{MessageID: pgtype.UUID{Bytes: msgID, Valid: true}, Now: ts(s.now())})
}

// Download is the answer to GET /api/media/{id}.
type Download struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	ExpiresIn   int    `json:"expires_in"`
}

// Download checks that viewer is in the chat, records the download and
// returns a short-lived link.
func (s *Service) Download(ctx context.Context, viewer, id uuid.UUID) (Download, error) {
	if s.store == nil {
		return Download{}, ErrDisabled
	}
	m, err := s.q.GetMedia(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Download{}, ErrNotFound
	}
	if err != nil {
		return Download{}, err
	}
	if m.Status != "ready" {
		return Download{}, ErrNotFound
	}
	chatID := uuid.UUID(m.ChatID.Bytes)
	var recipients []uuid.UUID // who must download it before the 24 h clock starts
	switch m.ChatType.String {
	case "private":
		if viewer != m.OwnerID && viewer != chatID {
			return Download{}, ErrNotFound
		}
		recipients = []uuid.UUID{chatID}
	case "public":
		members, err := s.members.Members(ctx, chatID)
		if err != nil || !slices.Contains(members, viewer) {
			return Download{}, ErrNotFound // also for a deleted group
		}
		if len(members) <= trackGroupsUpTo {
			recipients = slices.DeleteFunc(members, func(u uuid.UUID) bool { return u == m.OwnerID })
		}
	default:
		return Download{}, ErrNotFound
	}
	if m.DeletedAt.Valid {
		return Download{}, ErrExpired
	}
	now := s.now()
	if viewer != m.OwnerID {
		if err := s.q.RecordMediaDownload(ctx, database.RecordMediaDownloadParams{MediaID: id, UserID: viewer, Now: ts(now)}); err != nil {
			return Download{}, err
		}
		if len(recipients) > 0 {
			n, err := s.q.CountMediaDownloads(ctx, id)
			if err != nil {
				return Download{}, err
			}
			if int(n) >= len(recipients) {
				if err := s.q.MarkMediaDelivered(ctx, database.MarkMediaDeliveredParams{ID: id, Now: ts(now)}); err != nil {
					return Download{}, err
				}
			}
		}
	}
	u, err := s.store.PresignGet(key(id), downloadTTL)
	if err != nil {
		return Download{}, err
	}
	return Download{URL: u, ContentType: m.ContentType, Size: m.Size, ExpiresIn: int(downloadTTL.Seconds())}, nil
}

// Cleanup deletes expired files, then the least recently downloaded ones
// while storage is above the high mark. Run periodically.
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	if s.store == nil {
		return 0, nil
	}
	now := s.now()
	ids, err := s.q.MediaToDelete(ctx, database.MediaToDeleteParams{
		DeliveredBefore: ts(now.Add(-keepAfterAll)), Now: ts(now), PendingBefore: ts(now.Add(-pendingTTL)),
	})
	if err != nil {
		return 0, err
	}
	deleted := s.remove(ctx, ids)
	stored, err := s.q.MediaStoredBytes(ctx)
	if err != nil || stored <= lruHighBytes {
		return deleted, err
	}
	rows, err := s.q.LeastRecentlyUsedMedia(ctx)
	if err != nil {
		return deleted, err
	}
	var lru []uuid.UUID
	for _, r := range rows {
		if stored <= lruLowBytes {
			break
		}
		lru = append(lru, r.ID)
		stored -= r.Size
	}
	s.logger.WarnContext(ctx, "media storage above 9 GB: deleting least recently downloaded files", "files", len(lru))
	return deleted + s.remove(ctx, lru), nil
}

func (s *Service) remove(ctx context.Context, ids []uuid.UUID) int {
	var done []uuid.UUID
	for _, id := range ids {
		if err := s.store.Delete(ctx, key(id)); err != nil {
			s.logger.WarnContext(ctx, "failed to delete media file", "media_id", id, "error", err)
			continue
		}
		done = append(done, id)
	}
	if len(done) > 0 {
		if err := s.q.MarkMediaDeleted(ctx, database.MarkMediaDeletedParams{Ids: done, Now: ts(s.now())}); err != nil {
			s.logger.ErrorContext(ctx, "failed to mark media deleted", "error", err)
		}
	}
	return len(done)
}
