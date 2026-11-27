package stories

import (
	"strings"
	"testing"
)

func TestStickers(t *testing.T) {
	ok := Sticker{Type: "pigeon", Anim: "wave_01", X: 0.5, Y: 0.62, Size: 0.42, Rotation: -8}
	if b, err := stickersJSON(nil); b != nil || err != nil {
		t.Fatalf("none: %s %v", b, err)
	}
	b, err := stickersJSON([]Sticker{ok, {Type: "pigeon", Anim: "dance_01", X: 0, Y: 1, Size: 0.1, Rotation: 180}})
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
		if _, err := stickersJSON([]Sticker{s}); err == nil {
			t.Fatalf("%+v accepted", s)
		}
	}
	if _, err := stickersJSON([]Sticker{ok, ok, ok, ok}); err == nil {
		t.Fatal("4 stickers accepted")
	}
}
