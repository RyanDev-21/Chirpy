package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeDB struct {
	priv  []database.SearchPrivateMessagesRow
	grp   []database.SearchGroupMessagesRow
	lastP database.SearchPrivateMessagesParams
	lastG database.SearchGroupMessagesParams
	calls int
}

func (f *fakeDB) SearchPrivateMessages(_ context.Context, p database.SearchPrivateMessagesParams) ([]database.SearchPrivateMessagesRow, error) {
	f.lastP, f.calls = p, f.calls+1
	return f.priv, nil
}
func (f *fakeDB) SearchGroupMessages(_ context.Context, p database.SearchGroupMessagesParams) ([]database.SearchGroupMessagesRow, error) {
	f.lastG, f.calls = p, f.calls+1
	return f.grp, nil
}

type bans map[uuid.UUID]bool

func (b bans) Suspended(context.Context, []uuid.UUID) (map[uuid.UUID]bool, error) { return b, nil }

func at(min int) pgtype.Timestamp {
	return pgtype.Timestamp{Time: time.Date(2026, 11, 5, 10, min, 0, 0, time.UTC), Valid: true}
}
func txt(s string) pgtype.Text    { return pgtype.Text{String: s, Valid: true} }
func pg(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func TestMergeFilterAndValidate(t *testing.T) {
	me, bob, spammer, group := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	db := &fakeDB{
		priv: []database.SearchPrivateMessagesRow{
			{ID: uuid.New(), Content: txt("pizza tonight?"), FromID: pg(bob), ToID: pg(me), CreatedAt: at(30)},
			{ID: uuid.New(), Content: txt("pizza!"), FromID: pg(me), ToID: pg(bob), CreatedAt: at(10)},
		},
		grp: []database.SearchGroupMessagesRow{
			{ID: uuid.New(), Content: txt("PIZZA party"), FromID: pg(bob), GroupID: pg(group), CreatedAt: at(20)},
			{ID: uuid.New(), Content: txt("buy pizza here"), FromID: pg(spammer), GroupID: pg(group), CreatedAt: at(25)},
		},
	}
	svc := NewService(db, bans{spammer: true})
	ctx := context.Background()

	hits, err := svc.Search(ctx, me, Query{Q: "  PiZZa "})
	if err != nil {
		t.Fatal(err)
	}
	if db.lastP.Q != "pizza" || db.lastG.Q != "pizza" {
		t.Fatalf("query not lower-cased/trimmed: %q", db.lastP.Q)
	}
	if len(hits) != 3 || hits[0].CreatedAt.Minute() != 30 || hits[1].Type != "public" || hits[2].CreatedAt.Minute() != 10 {
		t.Fatalf("hits %+v", hits)
	}
	if hits[0].ChatID != bob || hits[2].ChatID != bob || hits[1].ChatID != group {
		t.Fatal("chat ids: the other person, or the group")
	}
	for _, h := range hits {
		if h.FromID == spammer {
			t.Fatal("suspended sender's message returned")
		}
	}

	// wildcards are literal
	_, _ = svc.Search(ctx, me, Query{Q: "100%_done"})
	if !strings.Contains(db.lastP.Q, `\%\_`) {
		t.Fatalf("not escaped: %q", db.lastP.Q)
	}

	// one chat only queries that kind; the limit is respected
	db.calls = 0
	hits, _ = svc.Search(ctx, me, Query{Q: "pi", Type: "private", ChatID: bob, Limit: 1})
	if db.calls != 1 || !db.lastP.WithUser.Valid || len(hits) != 1 {
		t.Fatalf("scoped: calls %d hits %d", db.calls, len(hits))
	}

	for _, q := range []Query{
		{Q: "pi"},                              // too short without a chat
		{Q: "p", Type: "private", ChatID: bob}, // too short even in one chat
		{Q: strings.Repeat("x", 101)},
		{Q: "pizza", Type: "public"}, // type without chat
		{Q: "pizza", ChatID: bob},    // chat without type
		{Q: "pizza", Type: "both", ChatID: bob},
		{Q: "pizza", Limit: 51},
	} {
		if _, err := svc.Search(ctx, me, q); !errors.Is(err, ErrBadQuery) && !errors.Is(err, ErrBadParams) {
			t.Errorf("%+v accepted: %v", q, err)
		}
	}
	// Thai and Burmese count characters, not bytes
	if _, err := svc.Search(ctx, me, Query{Q: "ข้าว"}); err != nil {
		t.Fatalf("thai: %v", err)
	}
}
