package codes

import "testing"

// Every code is labelled in both languages.
func TestEveryCodeHasBothLabels(t *testing.T) {
	for name, section := range sections {
		for code, p := range section {
			if p.en == "" || p.tr == "" {
				t.Errorf("%s.%s: en=%q tr=%q", name, code, p.en, p.tr)
			}
		}
	}
}
