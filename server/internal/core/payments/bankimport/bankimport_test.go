package bankimport

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

func TestParseAmount(t *testing.T) {
	cases := map[string]int64{
		"1.234,50":    123450,
		"-1.234,50":   -123450,
		"1,234.50":    123450,
		"1234.5":      123450,
		"1234,5":      123450,
		"12":          1200,
		"1.234":       123400,
		"1.234.567":   123456700,
		"₺ 12,00":     1200,
		"(12,00)":     -1200,
		"12,00-":      -1200,
		"0,05":        5,
		"250,00 TL":   25000,
		"1 234,50":    123450,
		"1.000.000,5": 100000050,
	}
	for in, want := range cases {
		got, err := ParseAmount(in)
		if err != nil || got != want {
			t.Errorf("ParseAmount(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1,234,5678x", "12,345678.123"} {
		if _, err := ParseAmount(in); err == nil {
			t.Errorf("ParseAmount(%q) should fail", in)
		}
	}
}

func TestParseCSVTurkish(t *testing.T) {
	csv := "\xef\xbb\xbfbooked_on;amount;counterparty;description;reference\n" +
		"05.03.2026;\"1.234,50\";Ayşe Yılmaz;Aidat;A-12-2026-03\n" +
		"06.03.2026;\"-250,00\";Banka;Masraf;\n" +
		"bad;12,00;x;y;z\n\n" +
		"07.03.2026;300,00;Şükrü Çelik;;B-3 2026-03\n"
	lines, errs, err := Parse("s.csv", []byte(csv), Mapping{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || len(errs) != 1 || errs[0].Field != "booked_on" {
		t.Fatalf("lines=%d errs=%v", len(lines), errs)
	}
	if l := lines[0]; l.BookedOn != "2026-03-05" || l.Amount != 123450 || l.Counterparty != "Ayşe Yılmaz" || l.Reference != "A-12-2026-03" {
		t.Errorf("line 0: %+v", l)
	}
	if lines[1].Amount != -25000 || lines[2].Amount != 30000 {
		t.Errorf("amounts: %d %d", lines[1].Amount, lines[2].Amount)
	}
}

func TestParseMappedHeaders(t *testing.T) {
	csv := "Tarih,Tutar,Açıklama,Bakiye\n2026-03-01,\"1.000,00\",Aidat A-1 2026-03,\"11.000,00\"\n2026-03-02,\"500,00\",Aidat,\"11.500,00\"\n"
	m := Mapping{Columns: map[string]string{"booked_on": "Tarih", "amount": "Tutar", "description": "Açıklama", "balance": "Bakiye"}}
	lines, errs, err := Parse("x.csv", []byte(csv), m)
	if err != nil || len(errs) != 0 || len(lines) != 2 {
		t.Fatalf("%v %v %d", err, errs, len(lines))
	}
	o, c := balancesOf(lines)
	if *o != 1000000 || *c != 1150000 {
		t.Errorf("balances %d %d", *o, *c)
	}
	if _, _, err := Parse("x.csv", []byte(csv), Mapping{Columns: map[string]string{"booked_on": "Nope", "amount": "Tutar"}}); err == nil {
		t.Error("missing column should fail")
	}
}

func TestParseXLSX(t *testing.T) {
	f := excelize.NewFile()
	rows := [][]any{{"booked_on", "amount", "counterparty", "description", "reference"}, {"01.03.2026", "1.234,50", "Ayşe", "Aidat", "A-1-2026-03"}, {"02.03.2026", 99.5, "Ali", "x", ""}}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		t.Fatal(err)
	}
	lines, errs, err := Parse("s.xlsx", b.Bytes(), Mapping{})
	if err != nil || len(errs) != 0 || len(lines) != 2 {
		t.Fatalf("%v %v %d", err, errs, len(lines))
	}
	if lines[0].Amount != 123450 || lines[1].Amount != 9950 {
		t.Errorf("amounts %d %d", lines[0].Amount, lines[1].Amount)
	}
}

func TestReferenceScheme(t *testing.T) {
	door, period, ok := ParseReference("A-12-2026-03")
	if !ok || door != "A-12" || period != "2026-03" {
		t.Errorf("%q %q %v", door, period, ok)
	}
	if got := BuildReference("A-12", "2026-03"); got != "A-12-2026-03" {
		t.Error(got)
	}
	door, period, ok = ParseReference("Aidat daire B 3 / 2026-11")
	if !ok || period != "2026-11" || !ReferenceHasDoor(door, "B-3") || ReferenceHasDoor(door, "3") == false {
		t.Errorf("%q %q %v", door, period, ok)
	}
	if ReferenceHasDoor("A-12", "A-1") {
		t.Error("A-1 must not match A-12")
	}
	if _, _, ok := ParseReference("havale"); ok {
		t.Error("no period")
	}
}

func TestNormalizeTurkish(t *testing.T) {
	if Normalize("İSMAİL ŞAHİN-Çelik") != Normalize("ismail sahin celik") {
		t.Errorf("%q", Normalize("İSMAİL ŞAHİN-Çelik"))
	}
}

func TestRank(t *testing.T) {
	u1, u2, p1, p2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cands := []Candidate{
		{UnitID: u1, Door: "A-1", Side: "owner", PersonID: &p1, PersonName: "Ayşe Yılmaz", OpenTotal: 100000, OpenCharges: []int64{100000}, Periods: []string{"2026-03"}},
		{UnitID: u1, Door: "A-1", Side: "tenant"},
		{UnitID: u2, Door: "A-12", Side: "owner", PersonID: &p2, PersonName: "Mehmet Kaya", OpenTotal: 100000, OpenCharges: []int64{100000}, Periods: []string{"2026-03"}},
	}
	got := Rank("AYSE YILMAZ", "", "A-1-2026-03", 100000, cands)
	if len(got) == 0 || got[0].UnitID != u1 || got[0].DebtorKind != "owner" || got[0].Score < 0.95 {
		t.Fatalf("best: %+v", got)
	}
	if len(got) > 1 && got[1].Score >= got[0].Score {
		t.Errorf("order: %+v", got)
	}
	// door alone, amount does not match: reference carries it
	got = Rank("", "", "A-12", 5, cands)
	if len(got) != 0 {
		t.Errorf("a bare door without period is not enough: %+v", got)
	}
	got = Rank("Mehmet Kaya", "", "", 5, cands)
	if len(got) != 1 || got[0].UnitID != u2 || got[0].Reason != "payer_name" {
		t.Errorf("name only: %+v", got)
	}
	got = Rank("", "", "", 100000, cands)
	if len(got) != 0 {
		t.Errorf("amount alone (0.2) is below the threshold: %+v", got)
	}
	if Rank("x", "", "A-1-2026-03", -100, cands) != nil {
		t.Error("outgoing lines get no suggestions")
	}
}
