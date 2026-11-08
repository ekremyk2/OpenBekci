package audit

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/platform/pii"
)

type thing struct {
	Name        string         `json:"name"`
	Count       int            `json:"count"`
	DisplayName string         `json:"display_name"`
	Contact     map[string]any `json:"contact"`
	Flags       []string       `json:"flags"`
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

const testSecret = "0123456789abcdef0123456789abcdef"

func TestMain(m *testing.M) {
	if err := SetPIIKey([]byte(testSecret)); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

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
	hex16 := regexp.MustCompile(`^k1:[0-9a-f]{32}$`) // key id, then 128 bits
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
	if c := d["display_name"]; c.HashBefore != "" || len(c.HashAfter) != len("k1:")+32 {
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
	// The specification, re-implemented independently of package pii:
	// HMAC-SHA256(HKDF(secret, info), "entity.field\x00" + json)[:16], prefixed "k1:".
	key, err := hkdf.Key(sha256.New, []byte(testSecret), nil, "openbekci/audit-pii/v1", 32)
	if err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("user.email\x00" + `"a@example.org"`))
	want := "k1:" + hex.EncodeToString(m.Sum(nil)[:16])
	if got := HashValue("user", "email", raw(`"a@example.org"`)); got != want {
		t.Fatalf("hash %q, want %q", got, want)
	}
	// domain separation: the same value in another field or entity hashes differently
	if HashValue("user", "phone", raw(`"a@example.org"`)) == want || HashValue("visitor", "email", raw(`"a@example.org"`)) == want {
		t.Error("hashes must be domain-separated by entity and field")
	}
	// another instance secret gives other hashes
	other, err := pii.NewHasher([]byte("another-instance-secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if other.Hash("user", "email", raw(`"a@example.org"`)) == want {
		t.Error("hashes must depend on the instance secret")
	}
	if h, _ := pii.NewHasher([]byte(testSecret)); !h.Verify(want, "user", "email", raw(`"a@example.org"`)) || h.Verify(want, "user", "email", raw(`"b@example.org"`)) {
		t.Error("Verify")
	}
	a, _ := Diff("user", map[string]any{"email": "x"}, map[string]any{"email": "a@example.org"})
	b, _ := Diff("user", map[string]any{"email": "y"}, map[string]any{"email": "a@example.org"})
	if a["email"].HashAfter != b["email"].HashAfter {
		t.Error("the same value must hash the same")
	}
	if HashValue("user", "email", nil) != "" {
		t.Error("an absent value hashes to the empty string")
	}
	// object key order does not matter
	x, _ := Diff("organization", nil, map[string]any{"tags": []any{map[string]any{"b": 1, "a": 2}}})
	y, _ := Diff("organization", nil, map[string]any{"tags": []any{map[string]any{"a": 2, "b": 1}}})
	if string(x["tags"].After) != string(y["tags"].After) {
		t.Error("canonical JSON must sort keys")
	}
}

// The cases of the F2 review probe: all of them used to be stored in clear.
func TestDiffNeverStoresPIIInNestedPluralOrCasedFields(t *testing.T) {
	state := func() map[string]any {
		return map[string]any{
			"billing":       map[string]any{"email": "billing@secret.example", "iban": "TR330006100519786457841326"},
			"contact_email": "ce@secret.example", "owner_phone": "+905551112233", "Email": "E@secret.example", "PHONE": "+905559998877",
			"emails": []any{"a@secret.example", "b@secret.example"}, "phones": []any{"+905550000000"},
			"owner": map[string]any{"national_id": "10000000146"},
		}
	}
	for _, entity := range []string{"organization", "widget"} {
		d, err := Diff(entity, nil, state())
		if err != nil {
			t.Fatal(err)
		}
		enc, _ := json.Marshal(d)
		for _, secret := range []string{"secret.example", "TR33", "+9055", "10000000146"} {
			if strings.Contains(string(enc), secret) {
				t.Errorf("%s: diff %s contains %q", entity, enc, secret)
			}
		}
		for _, k := range []string{"billing.email", "billing.iban", "contact_email", "owner_phone", "Email", "PHONE", "emails", "phones", "owner.national_id"} {
			if c, ok := d[k]; !ok || c.Before != nil || c.After != nil {
				t.Errorf("%s: %s = %+v, want a change without values", entity, k, c)
			}
		}
	}
}

func TestDiffUnknownEntityRecordsKeysOnly(t *testing.T) {
	d, err := Diff("widget", map[string]any{"label": "old"}, map[string]any{"label": "new", "size": 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Keys(); len(got) != 2 || got[0] != "label" || got[1] != "size" {
		t.Fatalf("keys = %v", got)
	}
	for k, c := range d {
		if c.Before != nil || c.After != nil || c.HashBefore != "" || c.HashAfter != "" {
			t.Errorf("%s leaks: %+v", k, c)
		}
	}
	// a known entity keeps its plain values
	d, _ = Diff("organization", map[string]any{"name": "old"}, map[string]any{"name": "new"})
	if string(d["name"].After) != `"new"` {
		t.Errorf("organization name: %+v", d["name"])
	}
}

func TestDiffRejectsNonObjects(t *testing.T) {
	if _, err := Diff("x", 42, nil); err == nil {
		t.Error("a scalar is not an entity")
	}
}
