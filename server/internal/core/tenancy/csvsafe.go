package tenancy

import "strings"

// HasFormulaPrefix reports whether a spreadsheet would read s as a formula
// (it starts with =, +, - or @, or a tab or carriage return).
func HasFormulaPrefix(s string) bool {
	return s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0]))
}

// CSVSafe prefixes a cell that starts like a formula with a single quote so
// Excel and Sheets show it as text (CSV injection).
func CSVSafe(s string) string {
	if HasFormulaPrefix(s) {
		return "'" + s
	}
	return s
}
