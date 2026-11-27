// Package characters: each user's low-poly character and the animations it
// can play. The app ships every model and animation; the server only keeps
// which pieces someone picked, checked against the catalog below (the
// frontend owns its content: entries are only ever added, never reordered,
// removed or reused, so old characters and messages keep working).
package characters

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

//go:embed catalog.json
var catalogJSON []byte

// Animation is one catalog animation; Fallback is shown (and read by
// screen readers) where it can't be played: "👋 waved".
type Animation struct {
	ID       string `json:"id"`
	Fallback string `json:"fallback"`
}

// Catalog lists every valid piece.
type Catalog struct {
	Version    int                 `json:"version"`
	Bodies     []string            `json:"bodies"`
	Slots      map[string][]string `json:"slots"`
	Palette    []string            `json:"palette"`
	Colors     []string            `json:"colors"`
	Animations []Animation         `json:"animations"`
}

var catalog = mustLoad(catalogJSON)

func mustLoad(raw []byte) Catalog {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		panic(fmt.Sprintf("characters: bad catalog.json: %v", err))
	}
	if len(c.Bodies) == 0 || len(c.Palette) == 0 {
		panic("characters: catalog.json needs bodies and a palette")
	}
	return c
}

// Current is the catalog (GET /api/characters/catalog).
func Current() Catalog { return catalog }

// RawCatalog is the catalog as served.
func RawCatalog() []byte { return catalogJSON }

// Fallback returns an animation's fallback text, and whether the id is in
// the catalog.
func Fallback(id string) (string, bool) {
	for _, a := range catalog.Animations {
		if a.ID == id {
			return a.Fallback, true
		}
	}
	return "", false
}

// twoPigeon animations need a second person (an invite and its answer), so
// they can't be used on their own, e.g. as a story sticker.
var twoPigeon = map[string]bool{"highfive_ask_01": true, "handshake_ask_01": true, "highfive_01": true, "handshake_01": true, "hanging_01": true}

// Solo reports whether id is a catalog animation one pigeon plays alone.
func Solo(id string) bool {
	_, ok := Fallback(id)
	return ok && !twoPigeon[id]
}

// Spec is a character: the body, a choice (or none) per slot, and a palette
// index per colour target.
type Spec struct {
	V      int                `json:"v"`
	Body   string             `json:"body"`
	Parts  map[string]*string `json:"parts"`
	Colors map[string]int     `json:"colors"`
}

var ErrBadCharacter = errors.New("character: v must be 1, body/parts from the catalog (each part in its slot, or null), colors palette indexes for the catalog's targets")

// Normalize checks raw against the catalog and returns it in a canonical
// form (sorted keys, empty maps instead of missing ones) and its version: a
// short hash that changes whenever the character does.
func Normalize(raw []byte) (canonical []byte, version string, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Spec
	if err := dec.Decode(&s); err != nil || dec.More() {
		return nil, "", ErrBadCharacter
	}
	if s.V != 1 || !slices.Contains(catalog.Bodies, s.Body) {
		return nil, "", ErrBadCharacter
	}
	if s.Parts == nil {
		s.Parts = map[string]*string{}
	}
	if s.Colors == nil {
		s.Colors = map[string]int{}
	}
	for slot, choice := range s.Parts {
		options, ok := catalog.Slots[slot]
		if !ok || (choice != nil && !slices.Contains(options, *choice)) {
			return nil, "", ErrBadCharacter
		}
	}
	for target, idx := range s.Colors {
		if !slices.Contains(catalog.Colors, target) || idx < 0 || idx >= len(catalog.Palette) {
			return nil, "", ErrBadCharacter
		}
	}
	canonical, err = json.Marshal(s) // map keys come out sorted
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:6]), nil
}
