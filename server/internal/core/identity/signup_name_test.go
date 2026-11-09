package identity

import (
	"strings"
	"testing"
)

func TestCleanName(t *testing.T) {
	good := map[string]string{
		"":                       "",
		"   ":                    "",
		"  Ayşe   Yılmaz ":       "Ayşe Yılmaz",
		"José":                  "José",  // NFC
		"a b\u0085c":             "a b c", // other line breaks become a space
		strings.Repeat("a", 200): strings.Repeat("a", 200),
	}
	for in, want := range good {
		if got, ok := cleanName(in); !ok || got != want {
			t.Errorf("cleanName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	bad := []string{
		strings.Repeat("a", 201),
		"a\x00b", "a\x07b", "a\x1bb", "a\u007fb", // NUL, BEL, ESC, DEL
		"a\u0080b", // C1 control
		"evil" + string(rune(0x202e)) + "gnp.exe", // right-to-left override
		"zero" + string(rune(0x200b)) + "width",   // zero-width space
		"", "bad\xffutf8",                        // private use, invalid UTF-8
	}
	for _, in := range bad {
		if got, ok := cleanName(in); ok {
			t.Errorf("cleanName(%q) accepted as %q", in, got)
		}
	}
}
