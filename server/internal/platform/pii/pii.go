// Package pii is the single registry of personal data fields (backend plan
// sections 4.13 and 16). Audit diffs hash registered fields instead of storing
// their values; a lint test (lint_test.go) fails when a struct field that
// looks like personal data is neither registered here nor allowlisted.
package pii

import "strings"

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
	{"person", "notes"},
	{"user", "email"},
	{"user", "phone"},
	{"user", "password_hash"},
	{"vehicle", "plate"},
	{"visitor", "name"},
	{"visitor", "phone"},
	{"visitor", "plate"},
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
}

// IsPII reports whether entity.field is registered as personal data.
func IsPII(entity, field string) bool {
	for _, f := range Registered {
		if f.Entity != "*" && f.Entity != entity {
			continue
		}
		if matches(f.Field, field) {
			return true
		}
	}
	return false
}

// IsPIIAnywhere reports whether field is registered for any entity.
func IsPIIAnywhere(field string) bool {
	for _, f := range Registered {
		if matches(f.Field, field) {
			return true
		}
	}
	return false
}

func matches(registered, field string) bool {
	if base, ok := strings.CutSuffix(registered, ".*"); ok {
		return field == base || strings.HasPrefix(field, base+".")
	}
	return registered == field
}

// Allowlist names struct fields whose names look like personal data but are
// not stored or diffed as entity state: transport inputs, delivery
// addresses of messages, and the like. Keys are "package.Struct.Field" (the
// package is the directory name); the value says why. Registering a field as
// PII is always preferable when it is persisted.
var Allowlist = map[string]string{}
