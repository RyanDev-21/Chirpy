// Package media handles photos, videos and files in messages. They live in
// Cloudflare R2 (free tier: 10 GB); phones upload and download them
// directly with short-lived signed links. To stay free:
//   - small uploads (up to 16 MB) are deleted 24 h after every recipient
//     downloaded them, after 7 days at most;
//   - large ones (16-100 MB) 24 h after upload, whatever happens;
//   - least-recently-used cleanup runs when storage nears 10 GB.
package media

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
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
	MaxVideoBytes  = 100 << 20
	MaxFileBytes   = 100 << 20
	DailyUserBytes = 300 << 20
	MaxPerMessage  = 10
	// LargeBytes: bigger uploads only stay largeLifetime.
	LargeBytes = 16 << 20
	// video length: up to 3 minutes when small, 10 when large (with slack
	// for rounding on the phone)
	maxSmallVideoMs = 3*60_000 + 5_000
	maxLargeVideoMs = 10*60_000 + 5_000

	// R2's free tier is 10 GB: new uploads stop at 9.5 GB, and the cleanup
	// deletes least-recently-downloaded files above 9 GB down to 8 GB.
	uploadCapBytes = 9_500 << 20
	lruHighBytes   = 9 << 30
	lruLowBytes    = 8 << 30

	maxLifetime   = 7 * 24 * time.Hour
	largeLifetime = 24 * time.Hour
	keepAfterAll  = 24 * time.Hour // after every recipient downloaded it
	pendingTTL    = time.Hour      // an upload link nobody attached
	uploadTTL     = 10 * time.Minute
	downloadTTL   = 5 * time.Minute
	// groups bigger than this skip per-member delivery tracking (7-day rule only)
	trackGroupsUpTo = 50
)

var (
	ErrDisabled      = errors.New("media is not configured on this server")
	ErrBadMedia      = errors.New("kind must be image (JPEG, PNG, WebP, HEIC; max 5 MB), video (MP4, MOV, WebM; max 100 MB; 3 min, or 10 min above 16 MB), voice (Opus/OGG, AAC/M4A, WebM, MP3; max 10 MB, 15 min, duration_ms required; waveform up to 128 values 0-255) or file (documents, archives, audio; max 100 MB; with file_name) with its size in bytes; width/height up to 8192")
	ErrBadFile       = errors.New("this kind of file can't be sent: allowed are documents (PDF, Word, Excel, PowerPoint, OpenDocument, text, CSV), archives (ZIP, 7z, RAR), e-books and audio; apps and programs are not")
	ErrDailyQuota    = errors.New("daily upload limit reached (300 MB per day)")
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
	// As measured by the sender's phone; 0 when unknown.
	DurationMs int `json:"duration_ms,omitempty"`
	Width      int `json:"width,omitempty"`
	Height     int `json:"height,omitempty"`
	// Thumbnail: a still is available at GET /api/media/{id}/thumbnail.
	Thumbnail bool `json:"thumbnail"`
	// FileName: files only, as the sender named it.
	FileName string `json:"file_name,omitempty"`
	// Waveform: voice messages, bar heights 0-255, for drawing the bubble
	// before the audio is downloaded.
	Waveform Waveform `json:"waveform,omitempty"`
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

func pu(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// Upload is the answer to POST /api/media.
type Upload struct {
	ID        uuid.UUID         `json:"media_id"`
	URL       string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresIn int               `json:"expires_in"`
}

// NewUpload checks limits and returns a link to PUT the file to.
// Dims are what the phone measured about the file (all optional).
type Dims struct {
	DurationMs int `json:"duration_ms"`
	Width      int `json:"width"`
	Height     int `json:"height"`
}

func (d Dims) ok(kind string, size int64) bool {
	if d.Width < 0 || d.Height < 0 || d.Width > 8192 || d.Height > 8192 || d.DurationMs < 0 {
		return false
	}
	switch kind {
	case "video":
		if size > LargeBytes {
			return d.DurationMs <= maxLargeVideoMs
		}
		return d.DurationMs <= maxSmallVideoMs
	case "image":
		return d.DurationMs == 0
	case "voice":
		return d.Width == 0 && d.Height == 0 && d.DurationMs > 0 && d.DurationMs <= maxVoiceMs
	}
	return d == Dims{} // files have no dimensions
}

// NewUploadRequest is POST /api/media.
type NewUploadRequest struct {
	Kind        string   `json:"kind"`
	ContentType string   `json:"content_type"`
	Size        int64    `json:"size"`
	FileName    string   `json:"file_name"` // files only
	Waveform    Waveform `json:"waveform"`  // voice messages only
	Dims
}

func optInt(n int) pgtype.Int4 { return pgtype.Int4{Int32: int32(n), Valid: n > 0} }

func (s *Service) NewUpload(ctx context.Context, owner uuid.UUID, req NewUploadRequest) (Upload, error) {
	if s.store == nil {
		return Upload{}, ErrDisabled
	}
	kind, contentType, size, dims := req.Kind, req.ContentType, req.Size, req.Dims
	var limit int64
	var fileName pgtype.Text
	switch kind {
	case "image":
		limit = MaxImageBytes
	case "video":
		limit = MaxVideoBytes
	case "voice":
		limit = MaxVoiceBytes
		if !voiceTypes[contentType] || req.FileName != "" {
			return Upload{}, ErrBadMedia
		}
	case "file":
		limit = MaxFileBytes
		name, ok := cleanFileName(req.FileName, contentType)
		if !ok {
			return Upload{}, ErrBadFile
		}
		fileName = pgtype.Text{String: name, Valid: true}
	default:
		return Upload{}, ErrBadMedia
	}
	if (kind == "image" || kind == "video") && (!allowedTypes[kind][contentType] || req.FileName != "") {
		return Upload{}, ErrBadMedia
	}
	if kind != "voice" && len(req.Waveform) > 0 {
		return Upload{}, ErrBadMedia
	}
	if size <= 0 || size > limit || !dims.ok(kind, size) {
		return Upload{}, ErrBadMedia
	}
	now := s.now()
	lifetime := maxLifetime
	if size > LargeBytes {
		lifetime = largeLifetime
	}
	used, err := s.q.MediaUploadedSince(ctx, database.MediaUploadedSinceParams{OwnerID: pu(owner), Since: ts(now.Add(-24 * time.Hour))})
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
		ID: id, OwnerID: pu(owner), Kind: kind, ContentType: contentType, Size: size,
		Now: ts(now), ExpiresAt: ts(now.Add(lifetime)),
		DurationMs: optInt(dims.DurationMs), Width: optInt(dims.Width), Height: optInt(dims.Height), FileName: fileName, Waveform: req.Waveform,
	}); err != nil {
		return Upload{}, err
	}
	u, err := s.store.PresignPut(key(id), contentType, size, uploadTTL)
	if err != nil {
		return Upload{}, err
	}
	telemetry.Media(ctx, "upload", kind)
	return Upload{ID: id, URL: u, Method: "PUT", ExpiresIn: int(uploadTTL.Seconds()),
		Headers: map[string]string{"Content-Type": contentType, "If-None-Match": "*"}}, nil
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
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (m.OwnerID != pu(owner) || m.Status != "pending" || m.DeletedAt.Valid)) {
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
		out = append(out, Attachment{ID: id, Kind: m.Kind, ContentType: m.ContentType, Size: m.Size, ExpiresAt: m.ExpiresAt.Time.UTC(),
			DurationMs: int(m.DurationMs.Int32), Width: int(m.Width.Int32), Height: int(m.Height.Int32), Thumbnail: m.HasThumbnail, FileName: m.FileName.String, Waveform: m.Waveform})
	}
	for _, id := range ids {
		n, err := s.q.AttachMedia(ctx, database.AttachMediaParams{
			ID: id, OwnerID: pu(owner), ChatType: pgtype.Text{String: chatType, Valid: true},
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
			ExpiresAt: r.ExpiresAt.Time.UTC(), Expired: r.DeletedAt.Valid,
			DurationMs: int(r.DurationMs.Int32), Width: int(r.Width.Int32), Height: int(r.Height.Int32), Thumbnail: r.HasThumbnail && !r.DeletedAt.Valid,
			FileName: r.FileName.String, Waveform: r.Waveform})
	}
	return out, nil
}

// ExpireGroup schedules a deleted group's media for removal.
func (s *Service) ExpireGroup(ctx context.Context, groupID uuid.UUID) {
	if err := s.q.ExpireMediaOfGroup(ctx, database.ExpireMediaOfGroupParams{GroupID: pu(groupID), Now: ts(s.now())}); err != nil {
		s.logger.WarnContext(ctx, "failed to expire media of deleted group", "error", err)
	}
}

// ExpireMessage schedules a deleted message's media for removal.
func (s *Service) ExpireMessage(ctx context.Context, msgID uuid.UUID) error {
	return s.q.ExpireMediaOfMessage(ctx, database.ExpireMediaOfMessageParams{MessageID: pgtype.UUID{Bytes: msgID, Valid: true}, Now: ts(s.now())})
}

// Download is the answer to GET /api/media/{id}.
type Download struct {
	URL         string `json:"url"`
	FileName    string `json:"file_name,omitempty"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	ExpiresIn   int    `json:"expires_in"`
}

// Download checks that viewer is in the chat, records the download and
// returns a short-lived link.
func (s *Service) Download(ctx context.Context, viewer, id uuid.UUID) (Download, error) {
	m, owner, recipients, err := s.access(ctx, viewer, id)
	if err != nil {
		return Download{}, err
	}
	now := s.now()
	if viewer != owner {
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
	u, err := s.store.PresignGet(key(id), m.ContentType, m.FileName.String, downloadTTL)
	if err != nil {
		return Download{}, err
	}
	telemetry.Media(ctx, "download", m.Kind)
	return Download{URL: u, FileName: m.FileName.String, ContentType: m.ContentType, Size: m.Size, ExpiresIn: int(downloadTTL.Seconds())}, nil
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
	deleted := s.remove(ctx, ids, "expired")
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
	return deleted + s.remove(ctx, lru, "lru"), nil
}

func (s *Service) remove(ctx context.Context, ids []uuid.UUID, reason string) int {
	var done []uuid.UUID
	thumbs := map[uuid.UUID]bool{}
	if len(ids) > 0 {
		if withThumb, err := s.q.ThumbnailsOf(ctx, ids); err == nil {
			for _, id := range withThumb {
				thumbs[id] = true
			}
		}
	}
	for _, id := range ids {
		if thumbs[id] {
			if err := s.store.Delete(ctx, thumbKey(id)); err != nil {
				s.logger.WarnContext(ctx, "failed to delete thumbnail", "media_id", id, "error", err)
				continue
			}
		}
		if err := s.store.Delete(ctx, key(id)); err != nil {
			s.logger.WarnContext(ctx, "failed to delete media file", "media_id", id, "error", err)
			continue
		}
		done = append(done, id)
		telemetry.Media(ctx, "delete", reason)
	}
	if len(done) > 0 {
		if err := s.q.MarkMediaDeleted(ctx, database.MarkMediaDeletedParams{Ids: done, Now: ts(s.now())}); err != nil {
			s.logger.ErrorContext(ctx, "failed to mark media deleted", "error", err)
		}
	}
	return len(done)
}

// access loads media that viewer may see: the two people of a private
// chat, or current members of the group. recipients are who must download
// it before the 24 h clock starts (nil for big groups).
func (s *Service) access(ctx context.Context, viewer, id uuid.UUID) (database.Medium, uuid.UUID, []uuid.UUID, error) {
	var none database.Medium
	if s.store == nil {
		return none, uuid.Nil, nil, ErrDisabled
	}
	m, err := s.q.GetMedia(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, uuid.Nil, nil, ErrNotFound
	}
	if err != nil {
		return none, uuid.Nil, nil, err
	}
	if m.Status != "ready" {
		return none, uuid.Nil, nil, ErrNotFound
	}
	chatID := uuid.UUID(m.ChatID.Bytes)
	owner := uuid.UUID(m.OwnerID.Bytes) // zero if the uploader deleted their account
	var recipients []uuid.UUID
	switch m.ChatType.String {
	case "private":
		if viewer != owner && viewer != chatID {
			return none, uuid.Nil, nil, ErrNotFound
		}
		recipients = []uuid.UUID{chatID}
	case "public":
		members, err := s.members.Members(ctx, chatID)
		if err != nil || !slices.Contains(members, viewer) {
			return none, uuid.Nil, nil, ErrNotFound // also for a deleted group
		}
		// like messages, media sent before the viewer joined isn't theirs to see
		ok, err := s.q.GroupMediaVisible(ctx, database.GroupMediaVisibleParams{MessageID: m.MessageID, GroupID: chatID, Viewer: viewer, UploadedAt: m.CreatedAt})
		if err != nil || !ok {
			return none, uuid.Nil, nil, ErrNotFound
		}
		if len(members) <= trackGroupsUpTo {
			recipients = slices.DeleteFunc(members, func(u uuid.UUID) bool { return u == owner })
		}
	default:
		return none, uuid.Nil, nil, ErrNotFound
	}
	if m.DeletedAt.Valid || !m.OwnerID.Valid {
		return none, uuid.Nil, nil, ErrExpired
	}
	return m, owner, recipients, nil
}

// --- video thumbnails ---

func thumbKey(id uuid.UUID) string { return "t/" + id.String() }

// MaxThumbnailUpload is the largest thumbnail image accepted (before the
// server shrinks it).
const MaxThumbnailUpload = 2 << 20

var (
	ErrNotVideo     = errors.New("thumbnails are for your own videos")
	ErrThumbnailSet = errors.New("this video already has a thumbnail")
)

// SetThumbnail stores a still frame for a video, made on the phone. The
// server re-encodes it (max 480 px, JPEG, metadata stripped), so only a
// real, small image ever reaches other phones.
func (s *Service) SetThumbnail(ctx context.Context, owner, id uuid.UUID, raw []byte) error {
	if s.store == nil {
		return ErrDisabled
	}
	m, err := s.q.GetMedia(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (m.OwnerID != pu(owner) || m.Kind != "video" || m.DeletedAt.Valid)) {
		return ErrNotVideo
	}
	if err != nil {
		return err
	}
	// set once: the preview can't be swapped after people have seen it
	if m.HasThumbnail {
		return ErrThumbnailSet
	}
	img, err := ProcessThumbnail(raw)
	if err != nil {
		return err
	}
	if err := s.store.Put(ctx, thumbKey(id), "image/jpeg", img); err != nil {
		return err
	}
	n, err := s.q.SetThumbnail(ctx, database.SetThumbnailParams{ID: id, OwnerID: pu(owner)})
	if err == nil && n == 0 {
		return ErrThumbnailSet // set or deleted meanwhile
	}
	return err
}

// Thumbnail is a short-lived link to a video's still, for the same people
// who may download the video (it doesn't count as a download).
func (s *Service) Thumbnail(ctx context.Context, viewer, id uuid.UUID) (Download, error) {
	m, _, _, err := s.access(ctx, viewer, id)
	if err != nil {
		return Download{}, err
	}
	if !m.HasThumbnail {
		return Download{}, ErrNotFound
	}
	u, err := s.store.PresignGet(thumbKey(id), "image/jpeg", "", downloadTTL)
	if err != nil {
		return Download{}, err
	}
	return Download{URL: u, ContentType: "image/jpeg", ExpiresIn: int(downloadTTL.Seconds())}, nil
}

// ErrForwardExpired: the original was already removed from storage; the
// app can upload its own copy instead.
var ErrForwardExpired = errors.New("this media has expired on the server; send it again from your phone")

// Duplicate copies media srcID for owner to attach to a forwarded message:
// storage copies the file itself (nothing is uploaded or downloaded), and
// the copy counts towards owner's daily quota like an upload. The copy is
// pending until attached, like a fresh upload.
func (s *Service) Duplicate(ctx context.Context, owner, srcID uuid.UUID) (uuid.UUID, error) {
	if s.store == nil {
		return uuid.Nil, ErrDisabled
	}
	m, err := s.q.GetMedia(ctx, srcID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if m.Status != "ready" || m.DeletedAt.Valid {
		return uuid.Nil, ErrForwardExpired
	}
	now := s.now()
	used, err := s.q.MediaUploadedSince(ctx, database.MediaUploadedSinceParams{OwnerID: pu(owner), Since: ts(now.Add(-24 * time.Hour))})
	if err != nil {
		return uuid.Nil, err
	}
	if used+m.Size > DailyUserBytes {
		return uuid.Nil, ErrDailyQuota
	}
	stored, err := s.q.MediaStoredBytes(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if stored+m.Size > uploadCapBytes {
		return uuid.Nil, ErrStorageFull
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	lifetime := maxLifetime
	if m.Size > LargeBytes {
		lifetime = largeLifetime
	}
	if err := s.q.CreateMedia(ctx, database.CreateMediaParams{
		ID: id, OwnerID: pu(owner), Kind: m.Kind, ContentType: m.ContentType, Size: m.Size,
		Now: ts(now), ExpiresAt: ts(now.Add(lifetime)),
		DurationMs: m.DurationMs, Width: m.Width, Height: m.Height, FileName: m.FileName, Waveform: m.Waveform,
	}); err != nil {
		return uuid.Nil, err
	}
	if err := s.store.Copy(ctx, key(srcID), key(id)); err != nil {
		if errors.Is(err, ErrNoObject) {
			return uuid.Nil, ErrForwardExpired
		}
		return uuid.Nil, err
	}
	if m.HasThumbnail {
		if err := s.store.Copy(ctx, thumbKey(srcID), thumbKey(id)); err == nil {
			if _, err := s.q.SetThumbnail(ctx, database.SetThumbnailParams{ID: id, OwnerID: pu(owner)}); err != nil {
				s.logger.WarnContext(ctx, "failed to mark forwarded thumbnail", "error", err)
			}
		}
	}
	telemetry.Media(ctx, "forward", m.Kind)
	return id, nil
}
