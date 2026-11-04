package chat

import (
	"errors"
	"unicode/utf8"
)

var ErrNotAnEmoji = errors.New("emoji must be a single emoji")

const maxEmojiBytes = 64

// isPictographic covers the emoji blocks (Extended_Pictographic in
// practice, without pulling in a Unicode segmentation library).
func isPictographic(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, // symbols & pictographs, emoticons, transport, supplemental, ext-A
		r >= 0x2600 && r <= 0x27BF,   // misc symbols, dingbats
		r >= 0x2B00 && r <= 0x2BFF,   // arrows, stars (⭐ ⬆)
		r >= 0x2190 && r <= 0x21FF,   // arrows (↔)
		r >= 0x2300 && r <= 0x23FF,   // misc technical (⌚ ⏰)
		r >= 0x1F000 && r <= 0x1F2FF, // mahjong, cards, enclosed (🅰 🈁)
		r == 0x00A9, r == 0x00AE, r == 0x203C, r == 0x2049, r == 0x2122, r == 0x2139,
		r == 0x3030, r == 0x303D, r == 0x3297, r == 0x3299:
		return true
	}
	return false
}

const (
	zwj        = 0x200D
	vs16       = 0xFE0F
	keycapMark = 0x20E3
)

func isSkinTone(r rune) bool { return r >= 0x1F3FB && r <= 0x1F3FF }
func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }
func isTag(r rune) bool      { return r >= 0xE0020 && r <= 0xE007F } // subdivision flags (🏴󠁧󠁢󠁳󠁣󠁴󠁿)

// ValidateEmoji accepts exactly one emoji: a pictograph with optional
// variation selector and skin tone, pictographs joined by ZWJ (👩‍💻),
// a flag (two regional indicators, or a tag sequence) or a keycap (1️⃣).
func ValidateEmoji(s string) error {
	if s == "" || len(s) > maxEmojiBytes || !utf8.ValidString(s) {
		return ErrNotAnEmoji
	}
	rs := []rune(s)

	// flag: exactly two regional indicators
	if isRegional(rs[0]) {
		if len(rs) == 2 && isRegional(rs[1]) {
			return nil
		}
		return ErrNotAnEmoji
	}
	// keycap: [0-9#*] FE0F? 20E3
	if r := rs[0]; (r >= '0' && r <= '9') || r == '#' || r == '*' {
		switch {
		case len(rs) == 2 && rs[1] == keycapMark,
			len(rs) == 3 && rs[1] == vs16 && rs[2] == keycapMark:
			return nil
		}
		return ErrNotAnEmoji
	}
	// pictograph sequences joined by ZWJ, each part optionally followed by
	// VS16 and a skin tone; a tag sequence may follow the base (🏴 + tags)
	expectBase := true
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case expectBase:
			if !isPictographic(r) {
				return ErrNotAnEmoji
			}
			expectBase = false
		case r == vs16 || isSkinTone(r) || isTag(r):
			// modifiers attach to the preceding base
		case r == zwj:
			expectBase = true
		default:
			return ErrNotAnEmoji // a second emoji or text without ZWJ
		}
	}
	if expectBase { // trailing ZWJ
		return ErrNotAnEmoji
	}
	return nil
}
