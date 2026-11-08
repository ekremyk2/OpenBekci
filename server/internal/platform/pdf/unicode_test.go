package pdf

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"testing"
)

// inflated returns the concatenated decompressed streams of a PDF except the font programs.
func inflated(t *testing.T, b []byte) string {
	t.Helper()
	var all strings.Builder
	rest := b
	for {
		i := bytes.Index(rest, []byte("stream\n"))
		if i < 0 {
			break
		}
		rest = rest[i+len("stream\n"):]
		j := bytes.Index(rest, []byte("\nendstream"))
		if j < 0 {
			break
		}
		if r, err := zlib.NewReader(bytes.NewReader(rest[:j])); err == nil {
			d, _ := io.ReadAll(r)
			if len(d) < 100000 {
				all.Write(d)
			}
		}
		rest = rest[j+len("\nendstream"):]
	}
	return all.String()
}

func TestUnicodeDocTurkishAndLira(t *testing.T) {
	d, err := NewUnicode("İhtarname")
	if err != nil {
		t.Fatal(err)
	}
	d.Line("ĞŞİ ğşı öüç", 12, true)
	d.Line("Borç: 1.234,50 ₺", 11, false)
	d.Paragraph(strings.Repeat("Ödenmediği takdirde yasal yollara başvurulacaktır. ", 250), 10, false)
	b := d.Bytes()
	if !bytes.HasPrefix(b, []byte("%PDF-1.5")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("not a PDF")
	}
	if !strings.Contains(d.PlainText(), "ğşı") || !strings.Contains(d.PlainText(), "₺") {
		t.Error("plain text lost the letters")
	}
	if !bytes.Contains(b, []byte("ActualText <FEFF20BA>")) && !strings.Contains(inflated(t, b), "ActualText <FEFF20BA>") {
		t.Error("the lira sign has no ActualText span")
	}
	s := inflated(t, b)
	for _, u := range []string{"011F", "015F", "0130", "0131", "011E", "015E"} {
		if !strings.Contains(s, "<"+u+">") {
			t.Errorf("ToUnicode lacks U+%s", u)
		}
	}
	if len(d.pages) < 2 {
		t.Errorf("expected the paragraph to flow onto more pages, got %d", len(d.pages))
	}
}
