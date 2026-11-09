package collections

import (
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/money"
)

func TestDocLang(t *testing.T) {
	for in, want := range map[string]string{"tr": "tr", "TR": "tr", "tr-TR": "tr", "tr_TR": "tr", "en": "en", "en-GB": "en", "de": "en", "": "en", "trx": "en"} {
		if got := DocLang(in); got != want {
			t.Errorf("DocLang(%q) = %q, want %q", in, got, want)
		}
	}
}

// Turkish stays exactly as it was; every other combination is formatted for
// its language and shows the sign of the site's own currency, never a lira.
func TestFormatAmountFollowsTheLanguageAndTheCurrency(t *testing.T) {
	for _, c := range []struct {
		minor int64
		cur   money.Code
		lang  string
		want  string
	}{
		{123450, money.TRY, "tr", "1.234,50 ₺"},
		{-5, money.TRY, "tr", "-0,05 ₺"},
		{0, money.TRY, "tr", "0,00 ₺"},
		{123450, money.USD, "en", "$1,234.50"},
		{123450, money.TRY, "en", "₺1,234.50"},
		{123450, money.EUR, "tr", "€1.234,50"},
	} {
		if got := FormatAmount(c.minor, c.cur, c.lang); got != c.want {
			t.Errorf("FormatAmount(%d, %s, %s) = %q, want %q", c.minor, c.cur, c.lang, got, c.want)
		}
	}
	if FormatAmount(123450, money.TRY, "tr") != FormatTL(123450) {
		t.Error("the Turkish lira in Turkish is FormatTL")
	}
}

func TestLetterTextInEachLanguage(t *testing.T) {
	d := letterData{SiteName: "Gül Sitesi", Date: "2 Jan 2026", DebtorName: "Ayşe Yılmaz", Door: "A-1", SideLabel: "the owner", AsOf: "9 Jan 2026", Total: "$1,050.00", Principal: "$1,000.00", LateFee: "$50.00", Days: LetterDays}
	en, err := letterText("en", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Gül Sitesi MANAGEMENT", "FORMAL PAYMENT NOTICE", "Date: 2 Jan 2026", "Dear Ayşe Yılmaz,", "as the owner (unit A-1)", "$1,050.00", "within 7 days"} {
		if !strings.Contains(en, want) {
			t.Errorf("the English letter lacks %q:\n%s", want, en)
		}
	}
	for _, turkish := range []string{"İHTARNAME", "Sayın", "Saygılarımızla", "kat", "Kanun", "tarihi itibarıyla"} {
		if strings.Contains(en, turkish) {
			t.Errorf("the English letter contains the Turkish %q", turkish)
		}
	}
	tr, err := letterText("tr", letterData{SiteName: "Gül Sitesi", Date: "02.01.2026", DebtorName: "Ayşe Yılmaz", Door: "A-1", SideLabel: "kat maliki", AsOf: "09.01.2026", Total: "1.050,00 ₺", Principal: "1.000,00 ₺", LateFee: "50,00 ₺", Days: LetterDays})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Gül Sitesi YÖNETİMİ", "İHTARNAME", "Tarih: 02.01.2026", "Sayın Ayşe Yılmaz,", "A-1 numaralı bağımsız bölümüne ilişkin kat maliki sıfatıyla", "09.01.2026 tarihi itibarıyla", "Toplam borcunuz 1.050,00 ₺ tutarındadır", "7 gün içinde", "Saygılarımızla,"} {
		if !strings.Contains(tr, want) {
			t.Errorf("the Turkish letter lacks %q:\n%s", want, tr)
		}
	}
}

func TestLetterDates(t *testing.T) {
	d := time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)
	if got := letterDate(d, "tr"); got != "09.03.2026" {
		t.Errorf("tr date %q", got)
	}
	if got := letterDate(d, "en"); got != "9 Mar 2026" {
		t.Errorf("en date %q", got)
	}
	if got := asOfDisplay("2026-03-09", "tr"); got != "09.03.2026" {
		t.Errorf("tr as-of %q", got)
	}
	if got := asOfDisplay("2026-03-09", "en"); got != "9 Mar 2026" {
		t.Errorf("en as-of %q", got)
	}
	if got := asOfDisplay("soon", "en"); got != "soon" {
		t.Errorf("a non-date stays as it is: %q", got)
	}
}
