package media

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// cleanFileName must never let through a name that isn't a plain, allowed
// file name, whatever the input.
func FuzzCleanFileName(f *testing.F) {
	for _, s := range []string{"report.pdf", "../x.pdf", "a\u202Efdp.exe", "C:\\a\\b.docx", "x.apk", ".pdf", "\x00.pdf"} {
		f.Add(s, "application/pdf")
	}
	f.Fuzz(func(t *testing.T, name, ct string) {
		got, ok := cleanFileName(name, ct)
		if !ok {
			return
		}
		ext := strings.ToLower(got[strings.LastIndexByte(got, '.')+1:])
		switch {
		case strings.ContainsAny(got, "/\\\"") || strings.ContainsFunc(got, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\u202e' || r == '\u200f' }):
			t.Fatalf("unsafe characters kept: %q", got)
		case !utf8.ValidString(got) && utf8.ValidString(name):
			t.Fatalf("made invalid UTF-8: %q", got)
		case utf8.RuneCountInString(got) > maxFileName:
			t.Fatalf("too long: %d", utf8.RuneCountInString(got))
		case fileTypes[ext] == nil:
			t.Fatalf("disallowed extension %q in %q", ext, got)
		}
	})
}

func FuzzWaveform(f *testing.F) {
	f.Add([]byte(`[0,1,255]`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var w Waveform
		if json.Unmarshal(raw, &w) != nil {
			return
		}
		if len(w) > maxWaveform {
			t.Fatalf("accepted %d values", len(w))
		}
		out, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		var back Waveform
		if err := json.Unmarshal(out, &back); err != nil || string(back) != string(w) {
			t.Fatalf("round trip %s -> %v", out, back)
		}
	})
}
