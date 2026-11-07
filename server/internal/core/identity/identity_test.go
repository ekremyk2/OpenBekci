package identity

import "testing"

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"+905321234567":      "+905321234567",
		"+90 532 123 45 67":  "+905321234567",
		"05321234567":        "+905321234567",
		"5321234567":         "+905321234567",
		"0090 532-123-45-67": "+905321234567",
		"(0532) 123 45 67":   "+905321234567",
		"+44 20 7946 0958":   "+442079460958",
	} {
		got, ok := NormalizePhone(in, "90")
		if !ok || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "123", "+0905321234567", "+90532123456789012", "53212x4567", "+"} {
		if got, ok := NormalizePhone(bad, "90"); ok {
			t.Errorf("NormalizePhone(%q) = %q, accepted", bad, got)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, ok := NormalizeEmail("  Ayse@Example.ORG "); !ok || got != "ayse@example.org" {
		t.Errorf("got %q, %v", got, ok)
	}
	for _, bad := range []string{"", "plain", "@x.org", "a@", "a@b", "a@@b.org", "a b@x.org", "a@b.org,c@d.org"} {
		if _, ok := NormalizeEmail(bad); ok {
			t.Errorf("NormalizeEmail(%q) accepted", bad)
		}
	}
}

func TestLinkURL(t *testing.T) {
	const base = "https://bekci.example.org/"
	for _, c := range []struct {
		client Client
		kind   LinkKind
		want   string
	}{
		{ClientWeb, LinkMagic, "https://bekci.example.org/login/magic?token=T"},
		{ClientMobile, LinkMagic, "openbekci://login/magic?token=T"},
		{ClientWeb, LinkInvite, "https://bekci.example.org/invite/T"},
		{ClientMobile, LinkInvite, "openbekci://invite/T"},
		{ClientWeb, LinkReset, "https://bekci.example.org/reset/T"},
		{ClientMobile, LinkReset, "openbekci://reset/T"},
	} {
		if got := LinkURL(base, c.client, c.kind, "T"); got != c.want {
			t.Errorf("LinkURL(%s, %s) = %q, want %q", c.client, c.kind, got, c.want)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if pe := ValidatePassword("123456789"); pe == nil || pe.Fields[0].Code != "too_short" {
		t.Errorf("9 chars: %v", pe)
	}
	if pe := ValidatePassword("1234567890"); pe != nil {
		t.Errorf("10 chars rejected: %v", pe)
	}
	// ten multi-byte characters count as ten
	if pe := ValidatePassword("şşşşşşşşşş"); pe != nil {
		t.Errorf("10 runes rejected: %v", pe)
	}
	long := make([]rune, MaxPasswordLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if pe := ValidatePassword(string(long)); pe == nil || pe.Fields[0].Code != "too_long" {
		t.Errorf("257 chars: %v", pe)
	}
}

func TestClipKeepsRunesWhole(t *testing.T) {
	if got := clip("abcdef", 3); got != "abc" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("aş", 2); got != "a" { // ş is two bytes; cutting at 2 would split it
		t.Errorf("clip split a rune: %q", got)
	}
}
