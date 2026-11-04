package chat

import "testing"

func TestValidateEmoji(t *testing.T) {
	ok := []string{
		"👍", "❤️", "😂", "🔥", "🙏", "⭐", "✅",
		"👍🏽",                     // skin tone
		"👩\u200d💻",               // ZWJ
		"👨\u200d👩\u200d👧\u200d👦", // family
		"🏳️\u200d🌈",              // flag sequence with VS16 + ZWJ
		"🇲🇲", "🇹🇭",               // regional-indicator flags
		"🏴󠁧󠁢󠁳󠁣󠁴󠁿",    // tag sequence (Scotland)
		"1️⃣", "#️⃣", // keycaps
		"🫶", // newer emoji
	}
	for _, e := range ok {
		if err := ValidateEmoji(e); err != nil {
			t.Errorf("ValidateEmoji(%q) rejected", e)
		}
	}
	bad := []string{
		"", "a", "ok", "👍👍", "👍 ", " 👍", "👍a", "🇲", "🇲🇲🇹", "1", "1️", "👩\u200d", "\u200d👩",
		"<script>", "\u200d", "😀́", string([]byte{0xff}),
	}
	for _, e := range bad {
		if err := ValidateEmoji(e); err == nil {
			t.Errorf("ValidateEmoji(%q) accepted", e)
		}
	}
}
