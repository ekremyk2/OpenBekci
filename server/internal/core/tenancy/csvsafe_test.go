package tenancy

import "testing"

func TestCSVSafePrefixesFormulaCells(t *testing.T) {
	for in, want := range map[string]string{
		"=HYPERLINK(\"http://evil\",\"x\")": "'=HYPERLINK(\"http://evil\",\"x\")",
		"+1":                                "'+1",
		"-2":                                "'-2",
		"@SUM(A1)":                          "'@SUM(A1)",
		"\tx":                               "'\tx",
		"\rx":                               "'\rx",
		"A-1":                               "A-1",
		"":                                  "",
		"Main":                              "Main",
	} {
		if got := CSVSafe(in); got != want {
			t.Errorf("CSVSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
