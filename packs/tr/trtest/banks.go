// SPDX-License-Identifier: AGPL-3.0-or-later

package trtest

import (
	"bytes"
	"embed"
	"strings"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

// The bank statement fixtures are SYNTHETIC: invented accounts, names and
// amounts in the shape the banks' exports are believed to have (fixtures/banks/README.md).

//go:embed fixtures/banks/*
var bankFiles embed.FS

// BankSample is one fixture file with what a correct parse yields.
type BankSample struct {
	Name     string
	Importer string
	Filename string
	Data     []byte
	// Text is the file's content as text (decoded from windows-1254, or the
	// cells of a workbook), for checking the raw rows of a parse against.
	Text       string
	WantLines  int
	WantTotal  int64
	WantErrors int
}

var bankManifest = []struct {
	name, importer, file string
	lines                int
	total                int64
	errors               int
}{
	{"ziraat csv, UTF-8, signed amount, preamble and a bad row", "tr.ziraat", "ziraat_hesap_hareketleri.csv", 5, -18250, 1},
	{"ziraat xlsx, numeric cells", "tr.ziraat", "ziraat_hesap_hareketleri.xlsx", 3, 201975, 0},
	{"isbank csv, windows-1254, debit and credit, newest first", "tr.isbank", "isbank_ekstre_cp1254.csv", 3, 75000, 0},
	{"isbank xlsx, date cells", "tr.isbank", "isbank_ekstre.xlsx", 3, 150050, 0},
}

// BankSamples returns the fixtures of one importer id ("tr.ziraat"); an empty id returns all.
func BankSamples(importer string) []BankSample {
	var out []BankSample
	for _, m := range bankManifest {
		if importer != "" && m.importer != importer {
			continue
		}
		data, err := bankFiles.ReadFile("fixtures/banks/" + m.file)
		if err != nil {
			panic(err)
		}
		out = append(out, BankSample{Name: m.name, Importer: m.importer, Filename: m.file, Data: data, Text: textOf(m.file, data),
			WantLines: m.lines, WantTotal: m.total, WantErrors: m.errors})
	}
	return out
}

func textOf(file string, data []byte) string {
	switch {
	case strings.HasSuffix(file, ".xlsx"):
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		defer func() { _ = f.Close() }()
		var b strings.Builder
		for _, sh := range f.GetSheetList() {
			rows, _ := f.GetRows(sh, excelize.Options{RawCellValue: true})
			for _, r := range rows {
				b.WriteString(strings.Join(r, "\n"))
				b.WriteByte('\n')
			}
		}
		return b.String()
	case strings.Contains(file, "cp1254"):
		out, err := charmap.Windows1254.NewDecoder().Bytes(data)
		if err != nil {
			panic(err)
		}
		return string(out)
	}
	return string(data)
}

// NotAStatement is a file no bank importer should accept.
var NotAStatement = []byte("name;age\nAyse;41\nMehmet;37\n")
