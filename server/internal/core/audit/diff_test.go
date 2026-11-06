package audit

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

type thing struct {
	Name        string         `json:"name"`
	Count       int            `json:"count"`
	DisplayName string         `json:"display_name"`
	Contact     map[string]any `json:"contact"`
	Flags       []string       `json:"flags"`
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestDiffValuesForPlainFieldsHashesForPII(t *testing.T) {
	before := thing{Name: "A", Count: 1, DisplayName: "Ayse Yilmaz", Contact: map[string]any{"email": "a@example.org", "city": "Izmir"}}
	after := thing{Name: "B", Count: 1, DisplayName: "Ayse Demir", Contact: map[string]any{"email": "b@example.org", "city": "Izmir"}, Flags: []string{"x"}}
	d, err := Diff("person", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if c := d["name"]; string(c.Before) != `"A"` || string(c.After) != `"B"` || c.HashBefore != "" {
		t.Errorf("name: %+v", c)
	}
	if _, ok := d["count"]; ok {
		t.Error("an unchanged field must not appear")
	}
	if _, ok := d["contact.city"]; ok {
		t.Error("an unchanged nested field must not appear")
	}
	hex16 := regexp.MustCompile(`^[0-9a-f]{16}$`)
	for _, field := range []string{"display_name", "contact.email"} {
		c, ok := d[field]
		if !ok {
			t.Fatalf("%s missing from %+v", field, d)
		}
		if c.Before != nil || c.After != nil {
			t.Errorf("%s leaks a value: %+v", field, c)
		}
		if !hex16.MatchString(c.HashBefore) || !hex16.MatchString(c.HashAfter) || c.HashBefore == c.HashAfter {
			t.Errorf("%s hashes: %+v", field, c)
		}
	}
	// nothing personal anywhere in the encoded diff
	enc, _ := json.Marshal(d)
	for _, secret := range []string{"Ayse", "Yilmaz", "Demir", "example.org"} {
		if strings.Contains(string(enc), secret) {
			t.Errorf("diff %s contains %q", enc, secret)
		}
	}
	if c := d["flags"]; string(c.Before) != "null" || string(c.After) != `["x"]` {
		t.Errorf("flags (nil slice to one element): %+v", c)
	}
}

func TestDiffCreateAndDelete(t *testing.T) {
	d, err := Diff("person", nil, map[string]any{"display_name": "Ayse", "role": "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if c := d["display_name"]; c.HashBefore != "" || len(c.HashAfter) != 16 {
		t.Errorf("created PII field: %+v", c)
	}
	if c := d["role"]; c.Before != nil || string(c.After) != `"owner"` {
		t.Errorf("created plain field: %+v", c)
	}
	d, err = Diff("person", map[string]any{"role": "owner"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := d["role"]; string(c.Before) != `"owner"` || c.After != nil {
		t.Errorf("deleted field: %+v", c)
	}
}

func TestHashValueIsCanonicalAndStable(t *testing.T) {
	// SHA-256("\"a@example.org\"") truncated to 16 hex digits; pinned so the
	// hashes stored in old audit rows stay comparable with new ones.
	if got := HashValue(raw(`"a@example.org"`)); got != "906e1f522edce22f" {
		t.Fatalf("hash %q", got)
	}
	a, _ := Diff("user", map[string]any{"email": "x"}, map[string]any{"email": "a@example.org"})
	b, _ := Diff("user", map[string]any{"email": "y"}, map[string]any{"email": "a@example.org"})
	if a["email"].HashAfter != b["email"].HashAfter {
		t.Error("the same value must hash the same")
	}
	if HashValue(nil) != "" {
		t.Error("an absent value hashes to the empty string")
	}
	// object key order does not matter
	x, _ := Diff("thing", nil, map[string]any{"tags": []any{map[string]any{"b": 1, "a": 2}}})
	y, _ := Diff("thing", nil, map[string]any{"tags": []any{map[string]any{"a": 2, "b": 1}}})
	if string(x["tags"].After) != string(y["tags"].After) {
		t.Error("canonical JSON must sort keys")
	}
}

func TestDiffRejectsNonObjects(t *testing.T) {
	if _, err := Diff("x", 42, nil); err == nil {
		t.Error("a scalar is not an entity")
	}
}
