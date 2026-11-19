package notes

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, ok := range []string{"at the gym", "🎂 today!", "👩\u200d💻 coding", "ไปเที่ยวทะเล", strings.Repeat("a", MaxLen)} {
		if err := Validate(ok); err != nil {
			t.Fatalf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", MaxLen+1), "two\nlines", "tab\there", "zero\u200bwidth", "rtl\u202eoverride", "bad\xffutf8"} {
		if Validate(bad) == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
