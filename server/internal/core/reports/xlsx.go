package reports

import (
	"bytes"
	"encoding/csv"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// amountCell matches a two-decimal amount as the CSV renderers write it ("1234.50" or "-1234,50").
var amountCell = regexp.MustCompile(`^-?\d+[.,]\d{2}$`)

// CSVToXLSX turns a report's CSV into a one-sheet workbook; two-decimal amounts become numeric cells.
func CSVToXLSX(raw []byte, sheet string) ([]byte, error) {
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return nil, err
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	for i, r := range rows {
		cells := make([]any, len(r))
		for j, c := range r {
			cells[j] = c
			if i > 0 && amountCell.MatchString(c) {
				if v, err := strconv.ParseFloat(strings.Replace(c, ",", ".", 1), 64); err == nil {
					cells[j] = v
				}
			}
		}
		name, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sheet, name, &cells); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
