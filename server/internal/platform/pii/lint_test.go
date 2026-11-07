package pii_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/openbekci/openbekci/server/internal/platform/pii"
)

var sensitiveName = regexp.MustCompile(`(?i)email|phone|mobile|msisdn|national|tckn|plate|plaka|note|address|iban|fullname|surname|birth|dob|tax|vergi`)

func snake(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i > 0 && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if i > 0 && (prevLower || nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestSensitiveStructFieldsAreClassified walks the server's Go source and
// fails for a struct field whose name looks like personal data
// (email, phone, national id, plate, note, address, iban) that is neither
// registered as PII for its struct nor allowlisted. Generated code is
// skipped: API DTOs and sqlc models mirror the spec and the tables, which are
// classified at their source.
func TestSensitiveStructFieldsAreClassified(t *testing.T) {
	root := filepath.Join("..", "..") // internal/
	used := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "gen", "dbgen", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		pkg := filepath.Base(filepath.Dir(path))
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				names := []string{}
				for _, id := range fld.Names {
					names = append(names, id.Name)
				}
				for _, name := range names {
					if !sensitiveName.MatchString(name) {
						continue
					}
					col := snake(name)
					if fld.Tag != nil {
						tag := reflect.StructTag(strings.Trim(fld.Tag.Value, "`"))
						if j, _, _ := strings.Cut(tag.Get("json"), ","); j != "" && j != "-" {
							col = j
						}
					}
					key := pkg + "." + ts.Name.Name + "." + name
					_, allowed := pii.Allowlist[key]
					used[key] = true
					if pii.IsPII(snake(ts.Name.Name), col) || allowed {
						continue
					}
					t.Errorf("%s: field %s looks like personal data but is not registered: add {%q, %q} to pii.Registered or %q to pii.Allowlist (with a reason)",
						fset.Position(fld.Pos()), key, snake(ts.Name.Name), col, key)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range pii.Allowlist {
		if !used[key] {
			t.Errorf("pii.Allowlist entry %q matches no struct field; remove it", key)
		}
	}
}

func TestIsPII(t *testing.T) {
	for _, c := range []struct {
		entity, field string
		want          bool
	}{
		{"person", "display_name", true},
		{"person", "contact", true},
		{"person", "contact.email", true},
		{"person", "contact.mobile.number", true},
		{"person", "contactless", false},
		{"user", "email", true},
		{"user", "locale", false},
		{"organization", "name", false},
		{"organization", "address", true}, // the "*" entries apply to every entity
		{"visitor", "name", true},
		{"unit", "name", false},
	} {
		if got := pii.IsPII(c.entity, c.field); got != c.want {
			t.Errorf("IsPII(%q, %q) = %v, want %v", c.entity, c.field, got, c.want)
		}
	}
}

// TestIsPIIWildcardMatchesLastSegment covers the shapes the first review found
// slipping through: nested paths, compound names, plurals, arrays, case.
func TestIsPIIWildcardMatchesLastSegment(t *testing.T) {
	for _, field := range []string{
		"billing.email", "billing.iban", "owner.national_id", "contact_email", "owner_phone",
		"Email", "PHONE", "emails[]", "phones[]", "emails", "Phones[0]", "owner.Emails[]",
		"ContactEmail", "OwnerPhone", "Mobile", "FullName", "full_name", "Surname", "BirthDate", "birth_date",
		"DOB", "tax_id", "TaxNumber", "vergi_no", "plaka", "plates", "Plate", "msisdn", "addresses", "tckn", "IBAN",
		"nested.deeper.owner.contact_phone",
	} {
		if !pii.IsPII("widget", field) {
			t.Errorf("IsPII(widget, %q) = false, want true", field)
		}
	}
	for _, field := range []string{"name", "template", "plateau", "emailing", "status", "description", "contactless", "taxonomy", "notebook"} {
		if pii.IsPII("widget", field) {
			t.Errorf("IsPII(widget, %q) = true, want false", field)
		}
	}
	// entity-specific entries keep their dotted-path meaning, case-insensitively
	if !pii.IsPII("person", "Contact.Email") || pii.IsPII("unit", "contact.name") {
		t.Error("entity-specific registrations")
	}
}

func TestKnownEntity(t *testing.T) {
	for entity, want := range map[string]bool{"person": true, "user": true, "organization": true, "widget": false, "unit": false, "*": false} {
		if got := pii.KnownEntity(entity); got != want {
			t.Errorf("KnownEntity(%q) = %v, want %v", entity, got, want)
		}
	}
}
