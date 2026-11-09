package chat

import "testing"

func TestNewCodeHas128BitsAndMatchesTheParser(t *testing.T) {
	a, _ := newCode()
	b, _ := newCode()
	if a == b || codeRe.FindString("/start "+a) != a || len(a) != len(codePrefix)+26 {
		t.Fatalf("bad codes %q %q", a, b)
	}
}
