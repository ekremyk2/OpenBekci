package tr

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/xuri/excelize/v2"
)

// A workbook that inflates far beyond its compressed size is refused before any sheet is read.
func TestReadXLSXRefusesZipBomb(t *testing.T) {
	var base bytes.Buffer
	if err := excelize.NewFile().Write(&base); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(base.Bytes()), int64(base.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		w, _ := zw.Create(zf.Name)
		r, _ := zf.Open()
		b, _ := io.ReadAll(r)
		_, _ = w.Write(b)
	}
	w, _ := zw.Create("xl/junk.bin")
	_, _ = w.Write(make([]byte, 100<<20))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readXLSX(out.Bytes(), nil); err == nil {
		t.Fatal("zip bomb was unpacked")
	}
}
