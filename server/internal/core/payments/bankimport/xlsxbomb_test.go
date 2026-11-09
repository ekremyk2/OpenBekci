package bankimport

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

// A workbook whose parts inflate to far more than the compressed upload is refused, not unpacked.
func TestReadXLSXRefusesZipBomb(t *testing.T) {
	f := excelize.NewFile()
	var base bytes.Buffer
	if err := f.Write(&base); err != nil {
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
		_, _ = w.Write(mustRead(t, r))
	}
	w, _ := zw.Create("xl/junk.bin")
	_, _ = w.Write(make([]byte, 100<<20)) // 100 MiB of zeros deflates to ~100 KiB
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if out.Len() > MaxFileBytes {
		t.Fatalf("test archive is %d bytes, over the upload cap", out.Len())
	}
	if _, err := readXLSX(out.Bytes(), nil); err == nil {
		t.Fatal("zip bomb was unpacked")
	}
}

func mustRead(t *testing.T, r interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	var b bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.Bytes()
		}
	}
}
