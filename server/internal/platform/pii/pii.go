// Package pii is the single registry of personal data fields (backend plan
// sections 4.13 and 16). Audit diffs hash registered fields instead of storing
// their values; a lint test (lint_test.go) fails when a struct field that
// looks like personal data is neither registered here nor allowlisted.
package pii

import (
	"regexp"
	"strings"
	"unicode"
)

// Field names one personal data field: an entity (table or struct, in
// snake_case) and a field name. The entity "*" matches every entity. A field
// ending in ".*" covers everything below it (a jsonb object such as
// person.contact), including the bare name.
type Field struct {
	Entity string
	Field  string
}

// Registered is the table of personal data fields. Add the field here in the
// same change that adds it to a table or struct.
var Registered = []Field{
	{"person", "display_name"},
	{"person", "contact.*"},
	{"person", "national_id"},
	{"person", "national_id_country"},
	{"person", "gate_contact_phone"},
	{"person", "notes"},
	{"site", "address.*"},
	{"site", "bank_details.*"},
	{"vendor", "name"},
	{"vendor", "tax_id"},
	{"vendor", "contact.*"},
	{"directory_entry", "phone"},
	{"unit_claim", "message"},
	{"unit_claim", "decision_note"},
	{"move_out", "notes"},
	{"user", "email"},
	{"user", "phone"},
	{"user", "password_hash"},
	{"vehicle", "plate"},
	{"visitor", "name"},
	{"visitor", "phone"},
	{"visitor", "plate"},
	{"visitor_entry", "name"},
	{"recurring_visitor", "name"},
	{"invitation", "email"},
	{"invitation", "phone"},
	{"*", "email"},
	{"*", "phone"},
	{"*", "national_id"},
	{"*", "tckn"},
	{"*", "plate"},
	{"*", "iban"},
	{"*", "address"},
	{"*", "note"},
	{"*", "notes"},
	{"*", "mobile"},
	{"*", "msisdn"},
	{"*", "full_name"},
	{"*", "surname"},
	{"*", "birth_date"},
	{"*", "dob"},
	{"*", "tax_id"},
	{"*", "tax_number"},
	{"*", "vergi_no"},
	{"*", "plaka"},
}

// PlainEntities are entities with no personal data that may store the values
// of their changes in clear in the audit trail (organization settings). Every
// other entity that is not registered above is unknown to the registry: the
// audit diff of an unknown entity records the changed key names only, never
// values (fail closed; see audit.Diff). Add an entity here in the same change
// that adds its audited view.
var PlainEntities = []string{"organization", "block", "unit", "allocation_key", "occupancy", "membership", "directory_consent", "facility", "facility_block", "booking"}

// KnownEntity reports whether entity may store non-PII values in clear: it
// has entries of its own in Registered, or is in PlainEntities.
func KnownEntity(entity string) bool {
	for _, e := range PlainEntities {
		if e == entity {
			return true
		}
	}
	for _, f := range Registered {
		if f.Entity != "*" && f.Entity == entity {
			return true
		}
	}
	return false
}

// IsPII reports whether entity.field is registered as personal data.
//
// Entries of a specific entity match the dotted path as registered
// (case-insensitively; "contact.*" covers everything below). The "*" entries
// match the LAST path segment of the field, case-insensitively and in
// plural or array form, also as the last word of a compound name: with
// {"*","email"} the fields email, Email, emails, emails[], billing.email,
// contact_email, ContactEmail and owner.Emails[] are personal data.
func IsPII(entity, field string) bool {
	lower := strings.ToLower(field)
	words := lastWords(field)
	for _, f := range Registered {
		if f.Entity == "*" {
			if matchesLast(f.Field, words) {
				return true
			}
			continue
		}
		if f.Entity == entity && matches(f.Field, lower) {
			return true
		}
	}
	return false
}

// IsPIIAnywhere reports whether field is registered for any entity.
func IsPIIAnywhere(field string) bool {
	lower := strings.ToLower(field)
	words := lastWords(field)
	for _, f := range Registered {
		if f.Entity == "*" && matchesLast(f.Field, words) || f.Entity != "*" && matches(f.Field, lower) {
			return true
		}
	}
	return false
}

func matches(registered, field string) bool {
	registered = strings.ToLower(registered)
	if base, ok := strings.CutSuffix(registered, ".*"); ok {
		return field == base || strings.HasPrefix(field, base+".")
	}
	return registered == field
}

var arraySuffix = regexp.MustCompile(`(\[[0-9]*\])+$`)

// lastWords splits the last segment of a dotted path into lower-case words:
// array suffixes are dropped, camelCase and snake_case are split, so
// "billing.OwnerPhones[]" gives [owner phones].
func lastWords(path string) []string {
	seg := path
	if i := strings.LastIndexByte(seg, '.'); i >= 0 {
		seg = seg[i+1:]
	}
	seg = arraySuffix.ReplaceAllString(seg, "")
	var b strings.Builder
	rs := []rune(seg)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ':
			b.WriteByte('_')
		case unicode.IsUpper(r):
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i > 0 && i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])
			if prevLower || nextLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return strings.Split(b.String(), "_")
}

// matchesLast reports whether the registered name (possibly several words,
// such as national_id) is the end of the words, allowing a plural last word.
func matchesLast(registered string, words []string) bool {
	reg := strings.Split(strings.ToLower(registered), "_")
	if len(words) < len(reg) {
		return false
	}
	tail := words[len(words)-len(reg):]
	for i, w := range tail {
		want := reg[i]
		if i < len(tail)-1 {
			if w != want {
				return false
			}
			continue
		}
		if w != want && w != want+"s" && w != want+"es" {
			return false
		}
	}
	return true
}

// Allowlist names struct fields whose names look like personal data but are
// not stored or diffed as entity state: transport inputs, delivery
// addresses of messages, and the like. Keys are "package.Struct.Field" (the
// package is the directory name); the value says why. Registering a field as
// PII is always preferable when it is persisted.
var Allowlist = map[string]string{
	"jobs.Notify.Template": "the name of a message template (it ends in -plate), not a vehicle plate",
}
