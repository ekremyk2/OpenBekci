package governance

import (
	_ "embed"
)

// The generated documents embed Noto Sans (SIL Open Font License 1.1, see
// fonts/OFL.txt): it covers Turkish letters and the Turkish lira sign (U+20BA),
// which the Go fonts used before did not (a decision such as "Aidat ₺2.000"
// printed as .notdef). fonts_test.go checks the glyphs.

//go:embed fonts/NotoSans-Regular.ttf
var notoRegular []byte

//go:embed fonts/NotoSans-Bold.ttf
var notoBold []byte
