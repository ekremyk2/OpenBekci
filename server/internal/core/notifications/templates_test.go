package notifications

import (
	"slices"
	"strings"
	"testing"
)

func TestRenderGolden(t *testing.T) {
	payload := map[string]any{"title": "Kitchen tap", "status": "Done", "site": "Gul Sitesi", "amount": "TRY 850,00", "unit": "A-1", "due_on": "2026-12-05"}
	cases := []struct {
		typ, locale string
		subject, in string
		sms         string
	}{
		{TypeTicketUpdated, "en", `Your request "Kitchen tap" is Done`, `"Kitchen tap" is now Done.`, `Gul Sitesi: "Kitchen tap" is now Done.`},
		{TypeTicketUpdated, "tr-TR", `"Kitchen tap" talebinin durumu: Done`, `"Kitchen tap" talebinin durumu: Done.`, `Gul Sitesi: "Kitchen tap" talebinin durumu: Done.`},
		{TypeChargeDueSoon, "en", `Payment due on 2026-12-05`, `TRY 850,00 for A-1 is due on 2026-12-05.`, `Gul Sitesi: TRY 850,00 for A-1 is due on 2026-12-05.`},
		{TypeChargeDueSoon, "tr", `2026-12-05 tarihinde ödeme günü`, `A-1 için TRY 850,00 tutarı 2026-12-05 tarihinde ödenmeli.`, `Gul Sitesi: A-1 için TRY 850,00 2026-12-05 tarihinde ödenmeli.`},
	}
	for _, c := range cases {
		r := Render(c.typ, c.locale, payload)
		if r.Subject != c.subject || r.Body != c.in || r.SMS != c.sms {
			t.Errorf("%s/%s: got %+v", c.typ, c.locale, r)
		}
	}
}

func TestRenderMissingKeysAndUnknownTypes(t *testing.T) {
	if r := Render(TypePaymentReceipt, "en", map[string]any{}); strings.Contains(r.Body, "no value") {
		t.Errorf("missing keys leak: %q", r.Body)
	}
	if r := Render("future.type", "xx", nil); r.Title != "future.type" || r.SMS != "future.type" {
		t.Errorf("unknown type: %+v", r)
	}
}

func TestEveryTypeHasTemplatesInBothLocales(t *testing.T) {
	for _, loc := range []string{"en", "tr"} {
		for _, typ := range append(slices.Clone(Vocabulary), Unmutable...) {
			if _, ok := catalogs[loc][loc][typ+"/body"]; !ok {
				t.Errorf("no %s template for %s", loc, typ)
			}
		}
	}
}

func TestSMSSegments(t *testing.T) {
	long := strings.Repeat("a", 161)
	for _, c := range []struct {
		text string
		want int
	}{
		{"", 0}, {"Water cut on Saturday", 1}, {strings.Repeat("a", 160), 1}, {long, 2},
		{"Su kesintisi cumartesi günü", 1}, // ü is in GSM 7-bit
		{"Toplantı yarın", 1},              // ı forces UCS-2 but is short
		{strings.Repeat("ş", 71), 2},
		{"{}", 1},
	} {
		if got := SMSSegments(c.text); got != c.want {
			t.Errorf("SMSSegments(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestDefaultChannelsFollowTheO5Table(t *testing.T) {
	for typ, want := range map[string]string{
		TypeChargeDueSoon: "in_app,sms", TypePaymentReceipt: "in_app,sms", TypeTicketUpdated: "in_app,email",
		TypeMeetingCalled: "in_app", TypeSignInCode: "sms,email",
	} {
		if got := strings.Join(DefaultChannels(typ), ","); got != want {
			t.Errorf("%s: %s, want %s", typ, got, want)
		}
	}
	if got := NormalizeChannels([]string{"sms", "bogus", "in_app", "sms"}); strings.Join(got, ",") != "in_app,sms" {
		t.Errorf("NormalizeChannels = %v", got)
	}
}

// Every type has the same non-empty parts in both locales; a part missing in
// one language must fail here, not render blank for a resident.
func TestLocaleCatalogsMatch(t *testing.T) {
	for typ := range catalogs["en"]["en"] {
		if _, ok := catalogs["tr"]["tr"][typ]; !ok {
			t.Errorf("tr is missing %s", typ)
		}
	}
	for typ := range catalogs["tr"]["tr"] {
		if _, ok := catalogs["en"]["en"][typ]; !ok {
			t.Errorf("en is missing %s", typ)
		}
	}
	for _, loc := range []string{"en", "tr"} {
		for key, tpl := range catalogs[loc][loc] {
			if strings.HasSuffix(key, "/body") || strings.HasSuffix(key, "/title") {
				var b strings.Builder
				if err := tpl.Execute(&b, map[string]any{}); err != nil || strings.TrimSpace(b.String()) == "" {
					t.Errorf("%s %s renders empty", loc, key)
				}
			}
		}
	}
}
