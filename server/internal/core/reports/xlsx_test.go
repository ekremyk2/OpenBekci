package reports

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestCSVToXLSX(t *testing.T) {
	b, err := CSVToXLSX([]byte("unit,total\nA-1,\"12,50\"\n"), "Debtors")
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := f.GetCellValue("Debtors", "B2"); v != "12.5" {
		t.Fatalf("B2 = %q", v)
	}
}
