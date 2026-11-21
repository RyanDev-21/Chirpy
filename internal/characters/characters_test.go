package characters

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	c, v, err := Normalize([]byte(`{"v":1,"body":"pigeon_round","parts":{"eyes":"happy_01","hat":null,"crest":"tuft_02"},"colors":{"wings":5,"body":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != `{"v":1,"body":"pigeon_round","parts":{"crest":"tuft_02","eyes":"happy_01","hat":null},"colors":{"body":0,"wings":5}}` || len(v) != 12 {
		t.Fatalf("canonical %s v %s", c, v)
	}
	// the same pieces in another order are the same character
	_, v2, _ := Normalize([]byte(`{"colors":{"body":0,"wings":5},"parts":{"hat":null,"crest":"tuft_02","eyes":"happy_01"},"body":"pigeon_round","v":1}`))
	if v2 != v {
		t.Fatalf("version %s != %s", v2, v)
	}
	if _, v3, _ := Normalize([]byte(`{"v":1,"body":"pigeon_tall"}`)); v3 == v {
		t.Fatal("a different character kept the version")
	}
	for _, bad := range []string{
		`{"v":2,"body":"pigeon_round"}`,
		`{"v":1,"body":"human"}`,
		`{"v":1,"body":"pigeon_round","parts":{"wings":"tuft_01"}}`, // no such slot
		`{"v":1,"body":"pigeon_round","parts":{"hat":"tuft_01"}}`,   // wrong slot
		`{"v":1,"body":"pigeon_round","colors":{"body":8}}`,         // past the palette
		`{"v":1,"body":"pigeon_round","colors":{"body":-1}}`,
		`{"v":1,"body":"pigeon_round","colors":{"beak":1}}`,
		`{"v":1,"body":"pigeon_round","extra":true}`,
		`{"v":1,"body":"pigeon_round"}{}`,
		`not json`,
	} {
		if _, _, err := Normalize([]byte(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestCatalogRules(t *testing.T) {
	c := Current()
	seen := map[string]bool{}
	for _, a := range c.Animations {
		if seen[a.ID] || a.Fallback == "" || !strings.Contains(a.Fallback, " ") {
			t.Fatalf("animation %+v: ids unique, fallback a short sentence", a)
		}
		seen[a.ID] = true
	}
	if f, ok := Fallback("fart_01"); !ok || f != "💨 farted" {
		t.Fatalf("fallback %q %v", f, ok)
	}
	if _, ok := Fallback("nope"); ok {
		t.Fatal("unknown animation")
	}
}
