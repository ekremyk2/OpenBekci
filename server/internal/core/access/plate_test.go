package access

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNormalizePlateFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/access-protocol/plate-normalization.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input      string  `json:"input"`
		Normalized *string `json:"normalized"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := NormalizePlate(c.Input)
		switch {
		case c.Normalized == nil && got != "":
			t.Errorf("%q: got %q, want invalid (empty)", c.Input, got)
		case c.Normalized != nil && got != *c.Normalized:
			t.Errorf("%q: got %q, want %q", c.Input, got, *c.Normalized)
		}
	}
}

func TestQRValueIsSHA256OfTheToken(t *testing.T) {
	// sha256("hello"), the value of the shared evaluation fixture
	if got := QRValue("hello"); got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("got %s", got)
	}
}
