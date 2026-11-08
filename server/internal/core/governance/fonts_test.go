package governance

import (
	"strings"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/sfnt"
)

// The embedded font has every Turkish letter and the lira sign: a glyph index
// of zero would print as .notdef.
func TestEmbeddedFontHasTurkishGlyphsAndLira(t *testing.T) {
	for name, data := range map[string][]byte{"regular": notoRegular, "bold": notoBold} {
		f, err := sfnt.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var buf sfnt.Buffer
		for _, r := range "ğĞşŞıİçÇöÖüÜâîû₺0123456789" {
			idx, err := f.GlyphIndex(&buf, r)
			if err != nil || idx == 0 {
				t.Errorf("%s font has no glyph for %q (U+%04X)", name, r, r)
			}
		}
	}
}

// A decision with the lira sign renders (the PDF is built, fonts embedded).
func TestPDFWithLiraSign(t *testing.T) {
	doc := Minutes(MinutesData{SiteName: "Çiçek Sitesi", Kind: "general_assembly", Locale: "tr", Loc: time.UTC,
		Items: []MinutesItem{{Position: 1, Title: "Bütçe", Decision: &MinutesDecision{Year: 2026, Seq: 1, Text: "Aidat ₺2.000 olarak belirlenmiştir", Outcome: "accepted"}}}})
	if !strings.Contains(doc.Text(), "₺2.000") {
		t.Fatal("the model lost the lira sign")
	}
	pdf, err := doc.PDF()
	if err != nil || !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("pdf: %v", err)
	}
}

// Table cells wrap: nothing of a long owner name or unit label is dropped.
func TestWrapLinesKeepsAllText(t *testing.T) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("noto", "", notoRegular)
	pdf.SetFont("noto", "", 9)
	name := "Muhammed Abdurrahman Çağlayan-Yıldırımoğlu Karaçalı Hacıibrahimoğulları"
	lines := wrapLines(pdf, name, 30)
	if len(lines) < 2 {
		t.Fatalf("a long name must wrap in a narrow column, got %v", lines)
	}
	if got := strings.ReplaceAll(strings.Join(lines, ""), " ", ""); got != strings.ReplaceAll(name, " ", "") {
		t.Fatalf("wrapping dropped text: %q from %q", got, name)
	}
	long := strings.Repeat("A", 80)
	if got := strings.Join(wrapLines(pdf, long, 30), ""); got != long {
		t.Fatalf("an unbreakable word was cut: %q", got)
	}
}

// The hazirun cetveli with long names and many rows renders to a PDF without truncating.
func TestAttendanceSheetPDFLongNames(t *testing.T) {
	var rows []SheetRow
	for i := 0; i < 60; i++ {
		rows = append(rows, SheetRow{Name: "Muhammed Abdurrahman Çağlayan-Yıldırımoğlu Karaçalı", Units: []string{"A-101", "B-202", "C-303", "D-404"}, ProxyFor: "Hacıibrahimoğulları Şükrü Efendi", Votes: 4, Share: 123.4567})
	}
	pdf, err := AttendanceSheet(AttendanceSheetData{SiteName: "Site", Locale: "tr", Loc: time.UTC, Rows: rows}).PDF()
	if err != nil || len(pdf) < 1000 || !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("pdf: %v (%d bytes)", err, len(pdf))
	}
}
