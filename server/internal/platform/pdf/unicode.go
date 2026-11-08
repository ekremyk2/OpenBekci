package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// UDoc writes documents with an embedded TrueType font (the Go fonts, BSD
// licensed) addressed by glyph ids, so every Latin Extended letter the font has
// (ğ ş İ ı ö ü ç ...) is real text that can be searched and copied. The Turkish
// lira sign ₺ is missing from the Go fonts; it is drawn as vector strokes
// inside a marked-content span whose ActualText is U+20BA, so text extraction
// still yields ₺.
type UDoc struct {
	title string
	pages []*bytes.Buffer
	cur   *bytes.Buffer
	// Y is the baseline of the next line (points from the bottom).
	Y     float64
	fonts [2]*ufont
	text  strings.Builder
}

type ufont struct {
	f    *sfnt.Font
	data []byte
	buf  sfnt.Buffer
	used map[uint16]rune
	wid  map[uint16]int
}

const lira = '₺'

// NewUnicode starts a document; the first page is added on the first line.
func NewUnicode(title string) (*UDoc, error) {
	d := &UDoc{title: title, Y: -1}
	for i, ttf := range [][]byte{goregular.TTF, gobold.TTF} {
		f, err := sfnt.Parse(ttf)
		if err != nil {
			return nil, err
		}
		d.fonts[i] = &ufont{f: f, data: ttf, used: map[uint16]rune{}, wid: map[uint16]int{}}
	}
	return d, nil
}

func (d *UDoc) page() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
	d.Y = PageH - marginTop
}

func (f *ufont) glyph(r rune) uint16 {
	g, err := f.f.GlyphIndex(&f.buf, r)
	if err != nil || g == 0 {
		g, _ = f.f.GlyphIndex(&f.buf, '?')
		r = '?'
	}
	gid := uint16(g)
	if _, ok := f.wid[gid]; !ok {
		adv, err := f.f.GlyphAdvance(&f.buf, g, fixed.I(1000), 0)
		if err != nil {
			adv = fixed.I(500)
		}
		f.wid[gid] = int(adv+32) >> 6
		f.used[gid] = r
	}
	return gid
}

// Width is the width of s in points at the given size.
func (d *UDoc) Width(s string, size float64, bold bool) float64 {
	f := d.fonts[b2i(bold)]
	var w float64
	for _, r := range s {
		if r == lira {
			w += 0.62 * size
			continue
		}
		w += float64(f.wid[f.glyph(r)]) * size / 1000
	}
	return w
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Text writes s at x on the current line without advancing.
func (d *UDoc) Text(x float64, s string, size float64, bold bool) {
	if d.cur == nil || d.Y < marginBottom {
		d.page()
	}
	d.text.WriteString(s)
	d.text.WriteByte('\n')
	fi := b2i(bold)
	f := d.fonts[fi]
	var run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		fmt.Fprintf(d.cur, "BT /F%d %.1f Tf %.2f %.2f Td <%s> Tj ET\n", fi+1, size, x, d.Y, run.String())
		x += d.Width(textOf(run.String(), f), size, bold)
		run.Reset()
	}
	for _, r := range s {
		if r == lira {
			flush()
			d.drawLira(x, d.Y, size)
			x += 0.62 * size
			continue
		}
		fmt.Fprintf(&run, "%04X", f.glyph(r))
	}
	flush()
}

// textOf recovers the text of a hex glyph run for width accounting.
func textOf(hex string, f *ufont) string {
	var sb strings.Builder
	for i := 0; i+4 <= len(hex); i += 4 {
		var g uint16
		_, _ = fmt.Sscanf(hex[i:i+4], "%04X", &g)
		sb.WriteRune(f.used[g])
	}
	return sb.String()
}

// drawLira draws ₺ with strokes: a stem with a curved foot and two slanted bars.
func (d *UDoc) drawLira(x, y, size float64) {
	p := func(u, v float64) string { return fmt.Sprintf("%.2f %.2f", x+u*size, y+v*size) }
	fmt.Fprintf(d.cur, "/Span <</ActualText <FEFF20BA>>> BDC\nq %.2f w 1 J 1 j\n", 0.075*size)
	fmt.Fprintf(d.cur, "%s m %s l\n", p(0.17, 0.70), p(0.17, 0.22))
	fmt.Fprintf(d.cur, "%s m %s %s %s c\n", p(0.17, 0.22), p(0.17, -0.02), p(0.50, -0.02), p(0.50, 0.26))
	fmt.Fprintf(d.cur, "%s m %s l\n", p(0.03, 0.30), p(0.58, 0.47))
	fmt.Fprintf(d.cur, "%s m %s l\nS Q\nEMC\n", p(0.03, 0.43), p(0.58, 0.60))
}

// Right writes s right-aligned to x.
func (d *UDoc) Right(x float64, s string, size float64, bold bool) {
	d.Text(x-d.Width(s, size, bold), s, size, bold)
}

// Line writes s at the left margin and advances one line.
func (d *UDoc) Line(s string, size float64, bold bool) {
	d.Text(MarginX, s, size, bold)
	d.Y -= size * 1.4
}

// Advance moves down by dy points.
func (d *UDoc) Advance(dy float64) { d.Y -= dy }

// Paragraph writes word-wrapped text within the page margins.
func (d *UDoc) Paragraph(s string, size float64, bold bool) {
	max := PageW - 2*MarginX
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			cand := strings.TrimSpace(line + " " + w)
			if line != "" && d.Width(cand, size, bold) > max {
				d.Line(line, size, bold)
				line = w
			} else {
				line = cand
			}
		}
		d.Line(line, size, bold)
	}
}

// PlainText returns everything written so far, one string per Text call (for tests).
func (d *UDoc) PlainText() string { return d.text.String() }

func deflate(b []byte) []byte {
	var out bytes.Buffer
	w := zlib.NewWriter(&out)
	_, _ = w.Write(b)
	_ = w.Close()
	return out.Bytes()
}

// Bytes renders the PDF.
func (d *UDoc) Bytes() []byte {
	if d.cur == nil {
		d.page()
	}
	var out bytes.Buffer
	var offs []int
	obj := func(body string) int {
		offs = append(offs, out.Len())
		n := len(offs)
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	stream := func(dict string, data []byte) int {
		offs = append(offs, out.Len())
		n := len(offs)
		fmt.Fprintf(&out, "%d 0 obj\n<< %s /Length %d >>\nstream\n", n, dict, len(data))
		out.Write(data)
		out.WriteString("\nendstream\nendobj\n")
		return n
	}
	out.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
	// fixed object numbers: 1 catalog, 2 pages, 3.. fonts, then pages
	catalog, pagesObj := 1, 2
	offs = make([]int, 0, 32)
	// reserve 1 and 2 by writing them last: write placeholders in order instead
	obj("<< /Type /Catalog /Pages 2 0 R >>") // 1
	pagesAt := len(offs)
	obj("PLACEHOLDER") // 2 (rewritten below)
	_ = catalog
	_ = pagesObj
	fontRefs := [2]int{}
	for i, f := range d.fonts {
		gids := make([]int, 0, len(f.used))
		for g := range f.used {
			gids = append(gids, int(g))
		}
		sort.Ints(gids)
		var w, cm strings.Builder
		w.WriteString("[")
		cm.WriteString("/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def /CMapName /Adobe-Identity-UCS def /CMapType 2 def 1 begincodespacerange <0000> <FFFF> endcodespacerange\n")
		for k := 0; k < len(gids); k += 100 {
			end := min(k+100, len(gids))
			fmt.Fprintf(&cm, "%d beginbfchar\n", end-k)
			for _, g := range gids[k:end] {
				fmt.Fprintf(&cm, "<%04X> <%04X>\n", g, f.used[uint16(g)])
			}
			cm.WriteString("endbfchar\n")
		}
		cm.WriteString("endcmap CMapName currentdict /CMap defineresource pop end end")
		for _, g := range gids {
			fmt.Fprintf(&w, "%d [%d] ", g, f.wid[uint16(g)])
		}
		w.WriteString("]")
		name := fmt.Sprintf("OpenBekciGo%d", i)
		ff := stream("/Filter /FlateDecode /Length1 "+fmt.Sprint(len(f.data)), deflate(f.data))
		tu := stream("/Filter /FlateDecode", deflate([]byte(cm.String())))
		desc := obj(fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags 32 /FontBBox [-200 -250 1200 950] /ItalicAngle 0 /Ascent 800 /Descent -200 /CapHeight 700 /StemV 80 /FontFile2 %d 0 R >>", name, ff))
		cid := obj(fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW 500 /W %s /CIDToGIDMap /Identity >>", name, desc, w.String()))
		fontRefs[i] = obj(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", name, cid, tu))
	}
	var kids []string
	for _, p := range d.pages {
		c := stream("/Filter /FlateDecode", deflate(p.Bytes()))
		pg := obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> >>", PageW, PageH, c, fontRefs[0], fontRefs[1]))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	info := obj(fmt.Sprintf("<< /Title %s /Producer (OpenBekci) >>", pdfString(d.title)))
	// rewrite object 2 in place by appending a replacement revision is not valid; instead build the final
	// file again with the page tree known: the page count is fixed, so patch the placeholder bytes.
	pages := fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
	return finalize(out.Bytes(), offs, pagesAt, pages, info)
}

func pdfString(s string) string {
	var sb strings.Builder
	sb.WriteString("<FEFF")
	for _, u := range encodeUTF16(s) {
		fmt.Fprintf(&sb, "%04X", u)
	}
	sb.WriteString(">")
	return sb.String()
}

func encodeUTF16(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// finalize replaces the placeholder of the page tree (object 2) and writes the cross-reference table.
// The placeholder is padded at creation so the replacement is appended as a new object body
// through an incremental layout: simplest correct approach is to rebuild the file with the real body.
func finalize(body []byte, offs []int, pagesAt int, pages string, info int) []byte {
	const ph = "2 0 obj\nPLACEHOLDER\nendobj\n"
	i := bytes.Index(body, []byte(ph))
	real := fmt.Sprintf("2 0 obj\n%s\nendobj\n", pages)
	shift := len(real) - len(ph)
	var out bytes.Buffer
	out.Write(body[:i])
	out.WriteString(real)
	out.Write(body[i+len(ph):])
	n := len(offs)
	for k := range offs {
		if k > pagesAt {
			offs[k] += shift
		}
	}
	x := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", n+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", n+1, info, x)
	return out.Bytes()
}
