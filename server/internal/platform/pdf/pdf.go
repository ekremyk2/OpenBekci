// Package pdf writes simple text documents (statements, receipts, certificates)
// as PDF 1.4 without dependencies. It uses the standard Helvetica fonts with an
// encoding that adds the Turkish letters (Ş ş Ğ ğ İ ı) on top of WinAnsi, so
// no font file is embedded and the output is small and deterministic.
package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// A4 page size in points and the margins used by the layout helpers.
const (
	PageW, PageH = 595.0, 842.0
	MarginX      = 50.0
	marginBottom = 50.0
	marginTop    = 56.0
)

var turkish = map[rune]byte{'Ş': 128, 'ş': 129, 'Ğ': 130, 'ğ': 131, 'İ': 132, 'ı': 133}

// Doc is a document under construction.
type Doc struct {
	pages []*bytes.Buffer
	cur   *bytes.Buffer
	// Y is the baseline of the next line (points from the bottom).
	Y     float64
	title string
}

// New starts a document; the first page is added on the first line.
func New(title string) *Doc { return &Doc{title: title, Y: -1} }

func (d *Doc) page() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
	d.Y = PageH - marginTop
}

func encode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(r))
		case r >= 32 && r < 127:
			b.WriteByte(byte(r))
		case turkish[r] != 0:
			fmt.Fprintf(&b, "\\%03o", turkish[r])
		case r >= 0xA0 && r <= 0xFF:
			fmt.Fprintf(&b, "\\%03o", r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// Text writes s at x on the current line without advancing.
func (d *Doc) Text(x float64, s string, size float64, bold bool) {
	if d.cur == nil || d.Y < marginBottom {
		d.page()
	}
	f := "F1"
	if bold {
		f = "F2"
	}
	fmt.Fprintf(d.cur, "BT /%s %.1f Tf %.1f %.1f Td (%s) Tj ET\n", f, size, x, d.Y, encode(s))
}

// Right writes s right-aligned to x (an estimate from the average glyph width).
func (d *Doc) Right(x float64, s string, size float64, bold bool) {
	d.Text(x-Width(s, size), s, size, bold)
}

// Width estimates the width of s in points (Helvetica averages about 0.5 em).
func Width(s string, size float64) float64 { return float64(len([]rune(s))) * size * 0.5 }

// Skip moves down by h points, starting a new page when the page is full.
func (d *Doc) Skip(h float64) {
	if d.cur == nil {
		d.page()
	}
	d.Y -= h
	if d.Y < marginBottom {
		d.page()
	}
}

// Line writes one line at the left margin and moves down.
func (d *Doc) Line(s string, size float64, bold bool) {
	d.Text(MarginX, s, size, bold)
	d.Skip(size + 4)
}

// Rule draws a horizontal line across the page at the current position.
func (d *Doc) Rule() {
	if d.cur == nil {
		d.page()
	}
	fmt.Fprintf(d.cur, "0.5 w %.1f %.1f m %.1f %.1f l S\n", MarginX, d.Y+8, PageW-MarginX, d.Y+8)
}

// Bytes renders the document.
func (d *Doc) Bytes() []byte {
	if d.cur == nil {
		d.page()
	}
	var out bytes.Buffer
	offsets := []int{}
	obj := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n")
	n := len(d.pages)
	// 1 catalog, 2 pages, 3 and 4 fonts, 5 info; then page and content pairs
	kids := make([]string, n)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", 6+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	enc := "<< /Type /Encoding /BaseEncoding /WinAnsiEncoding /Differences [128 /Scedilla /scedilla /Gbreve /gbreve /Idotaccent /dotlessi] >>"
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding " + enc + " >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding " + enc + " >>")
	obj(fmt.Sprintf("<< /Title (%s) /Producer (OpenBekci) >>", encode(d.title)))
	for i, p := range d.pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", PageW, PageH, 7+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", p.Len(), p.String()))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 5 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}
