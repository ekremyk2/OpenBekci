package people

import (
	"os"
	"testing"
)

func TestValidTCKN(t *testing.T) {
	for _, ok := range []string{"10000000146", "11111111110"} {
		if !ValidTCKN(ok) {
			t.Errorf("%s is a valid TCKN", ok)
		}
	}
	for _, bad := range []string{"", "1234567890", "12345678901", "01234567890", "10000000147", "1000000014a", "100000001460"} {
		if ValidTCKN(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if !ValidateNationalID("DE", "T22000129") || ValidateNationalID("DE", "x") {
		t.Error("generic ids need 4 to 30 letters or digits")
	}
	if ValidateNationalID("TR", "T22000129") {
		t.Error("TR numbers are checksum-validated")
	}
}

func TestMaskNationalID(t *testing.T) {
	if got := MaskNationalID("10000000146"); got != "*******0146" {
		t.Errorf("mask = %q", got)
	}
}

func TestParseMoney(t *testing.T) {
	for in, want := range map[string]int64{"1.250,50": 125050, "1,250.50": 125050, "-300": -30000, "12,5": 1250, "0": 0, "99 TL": 9900} {
		got, ok := ParseMoney(in)
		if !ok || got != want {
			t.Errorf("ParseMoney(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "1e999"} {
		if _, ok := ParseMoney(bad); ok {
			t.Errorf("ParseMoney(%q) should fail", bad)
		}
	}
}

func TestMapColumnsAliasesAndMapping(t *testing.T) {
	idx, used, err := MapColumns([]string{"Daire No", "Blok", "Alan", "Malik", "Plaka"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if idx["door_number"] != 0 || idx["block"] != 1 || idx["area_m2"] != 2 || idx["owner_name"] != 3 || idx["plates"] != 4 || used["door_number"] != "Daire No" {
		t.Errorf("aliases: %v", idx)
	}
	idx, _, err = MapColumns([]string{"X", "Kisi"}, map[string]string{"door_number": "x", "owner_name": "KİŞİ"})
	if err != nil || idx["door_number"] != 0 || idx["owner_name"] != 1 {
		t.Errorf("explicit mapping: %v %v", idx, err)
	}
	if _, _, err := MapColumns([]string{"a", "b"}, nil); err == nil {
		t.Error("a file without a door number column is refused")
	}
	if _, _, err := MapColumns([]string{"a"}, map[string]string{"nope": "a"}); err == nil {
		t.Error("an unknown canonical column is refused")
	}
}

func TestParseSheetDelimiters(t *testing.T) {
	for _, in := range []string{"door_number,area_m2\nA-1,80\n", "door_number;area_m2\nA-1;80\n", "\xef\xbb\xbfdoor_number\tarea_m2\nA-1\t80\n"} {
		rows, err := ParseSheet("x.csv", []byte(in))
		if err != nil || len(rows) != 2 || len(rows[1]) != 2 || rows[1][0] != "A-1" {
			t.Errorf("ParseSheet(%q) = %v, %v", in, rows, err)
		}
	}
}

func TestGoldenFixtureParses(t *testing.T) {
	data, err := os.ReadFile("../../httpapi/testdata/golden_site.csv")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseSheet("golden_site.csv", data)
	if err != nil || len(rows) != 26 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	idx, _, err := MapColumns(rows[0], nil)
	if err != nil || len(idx) != len(ImportColumns)-1 { // the fixture has no tenant_email column
		t.Fatalf("columns = %d of %d, %v", len(idx), len(ImportColumns), err)
	}
	for i, r := range rows {
		if len(r) != len(rows[0]) {
			t.Errorf("row %d has %d fields, want %d", i+1, len(r), len(rows[0]))
		}
	}
}

func TestScopedTokenRoundTrip(t *testing.T) {
	org := [16]byte{1, 2, 3}
	tok, hash := newScopedToken(org, 32)
	got, ok := parseScopedToken(tok)
	if !ok || got != org || len(hash) != 32 {
		t.Errorf("round trip: %v %v", got, ok)
	}
	for _, bad := range []string{"", "x", "abc.def", ".", "a.b.c" + string(make([]byte, 300))} {
		if _, ok := parseScopedToken(bad); ok {
			t.Errorf("parseScopedToken(%q) should fail", bad)
		}
	}
}
