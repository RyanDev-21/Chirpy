package stories

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestStickers(t *testing.T) {
	ok := Sticker{Type: "pigeon", Anim: "wave_01", X: 0.5, Y: 0.62, Size: 0.42, Rotation: -8}
	if b, _, err := stickersJSON(nil); b != nil || err != nil {
		t.Fatalf("none: %s %v", b, err)
	}
	b, _, err := stickersJSON([]Sticker{ok, {Type: "pigeon", Anim: "dance_01", X: 0, Y: 1, Size: 0.1, Rotation: 180}})
	if err != nil || !strings.Contains(string(b), `"anim":"wave_01"`) {
		t.Fatalf("valid: %s %v", b, err)
	}
	bad := []Sticker{
		{Type: "emoji", Anim: "wave_01", X: .5, Y: .5, Size: .3},
		{Type: "pigeon", Anim: "moonwalk_99", X: .5, Y: .5, Size: .3},
		{Type: "pigeon", Anim: "highfive_ask_01", X: .5, Y: .5, Size: .3}, // needs two pigeons
		{Type: "pigeon", Anim: "hanging_01", X: .5, Y: .5, Size: .3},
		{Type: "pigeon", Anim: "wave_01", X: 1.1, Y: .5, Size: .3},
		{Type: "pigeon", Anim: "wave_01", X: .5, Y: -0.1, Size: .3},
		{Type: "pigeon", Anim: "wave_01", X: .5, Y: .5, Size: 0.05},
		{Type: "pigeon", Anim: "wave_01", X: .5, Y: .5, Size: 0.9},
		{Type: "pigeon", Anim: "wave_01", X: .5, Y: .5, Size: .3, Rotation: 181},
	}
	for _, s := range bad {
		if _, _, err := stickersJSON([]Sticker{s}); err == nil {
			t.Fatalf("%+v accepted", s)
		}
	}
	if _, _, err := stickersJSON([]Sticker{ok, ok, ok, ok}); err == nil {
		t.Fatal("4 stickers accepted")
	}
}

func TestMentionStickers(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	m := func(id *uuid.UUID) Sticker { return Sticker{Type: "mention", UserID: id, X: .5, Y: .3, Size: .3} }
	_, ids, err := stickersJSON([]Sticker{m(&a), m(&b), m(&a)})
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids %v err %v", ids, err)
	}
	nobody := uuid.Nil
	for _, bad := range []Sticker{m(nil), m(&nobody), {Type: "mention", UserID: &a, Anim: "wave_01", X: .5, Y: .5, Size: .3}, {Type: "pigeon", Anim: "wave_01", UserID: &a, X: .5, Y: .5, Size: .3}} {
		if _, _, err := stickersJSON([]Sticker{bad}); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	many := []Sticker{}
	for range MaxMentions + 1 {
		id := uuid.New()
		many = append(many, m(&id))
	}
	if _, _, err := stickersJSON(many); err == nil {
		t.Fatal("11 mentions accepted")
	}
	// names sent by a client aren't stored
	n := m(&a)
	n.Name = "fake"
	if raw, _, _ := stickersJSON([]Sticker{n}); strings.Contains(string(raw), "fake") {
		t.Fatalf("stored %s", raw)
	}
}
