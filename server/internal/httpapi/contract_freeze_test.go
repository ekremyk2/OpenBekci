package httpapi

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The /v1 freeze (docs/api/versioning.md, ADR-0003): the served spec may only
// grow. Every committed snapshot api/snapshots/v1.*.yaml is compared with
// api/openapi.yaml; a stable operation that disappeared, or a field, type or
// enum value that was removed or changed in a request or response, fails. Only
// operations that were x-stability: stable in the snapshot are held; preview
// operations may still change.
func TestV1FreezeAgainstSnapshots(t *testing.T) {
	cur := loadSpec(t)
	snaps, err := filepath.Glob(filepath.Join("..", "..", "..", "api", "snapshots", "v1.*.yaml"))
	if err != nil || len(snaps) == 0 {
		t.Fatalf("no snapshot api/snapshots/v1.*.yaml (%v)", err)
	}
	for _, f := range snaps {
		old, err := openapi3.NewLoader().LoadFromFile(f)
		if err != nil {
			t.Fatalf("load %s: %v", f, err)
		}
		for _, b := range breakingChanges(old, cur) {
			t.Errorf("%s: breaking change against %s: %s (additive changes only on /v1; see docs/api/versioning.md)", "api/openapi.yaml", filepath.Base(f), b)
		}
	}
}

// breakingChanges lists what a client of old would lose with cur.
func breakingChanges(old, cur *openapi3.T) []string {
	var out []string
	for path, item := range old.Paths.Map() {
		for method, op := range item.Operations() {
			if s, _ := op.Extensions["x-stability"].(string); s == "preview" {
				continue
			}
			where := method + " " + path
			cItem := cur.Paths.Find(path)
			if cItem == nil || cItem.GetOperation(method) == nil {
				out = append(out, where+": operation removed")
				continue
			}
			cOp := cItem.GetOperation(method)
			if cOp.OperationID != op.OperationID {
				out = append(out, fmt.Sprintf("%s: operationId %q became %q", where, op.OperationID, cOp.OperationID))
			}
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				if cOp.RequestBody == nil || cOp.RequestBody.Value == nil {
					out = append(out, where+": request body removed")
				} else {
					out = append(out, diffContent(where+" request", op.RequestBody.Value.Content, cOp.RequestBody.Value.Content)...)
				}
			}
			for code, r := range op.Responses.Map() {
				cr := cOp.Responses.Map()[code]
				if cr == nil {
					out = append(out, fmt.Sprintf("%s: response %s removed", where, code))
					continue
				}
				if r.Value != nil && cr.Value != nil {
					out = append(out, diffContent(fmt.Sprintf("%s response %s", where, code), r.Value.Content, cr.Value.Content)...)
				}
			}
			for _, p := range op.Parameters {
				found := false
				for _, cp := range cOp.Parameters {
					if cp.Value.In == p.Value.In && cp.Value.Name == p.Value.Name {
						found = true
						out = append(out, diffSchema(fmt.Sprintf("%s parameter %s", where, p.Value.Name), p.Value.Schema, cp.Value.Schema, map[*openapi3.Schema]bool{})...)
					}
				}
				if !found {
					out = append(out, fmt.Sprintf("%s: parameter %s removed", where, p.Value.Name))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func diffContent(where string, old, cur openapi3.Content) []string {
	var out []string
	for mt, o := range old {
		c := cur[mt]
		if c == nil {
			out = append(out, fmt.Sprintf("%s: media type %s removed", where, mt))
			continue
		}
		out = append(out, diffSchema(where, o.Schema, c.Schema, map[*openapi3.Schema]bool{})...)
	}
	return out
}

func diffSchema(where string, o, c *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) []string {
	if o == nil || o.Value == nil {
		return nil
	}
	if c == nil || c.Value == nil {
		return []string{where + ": schema removed"}
	}
	if seen[o.Value] {
		return nil
	}
	seen[o.Value] = true
	var out []string
	os, cs := o.Value, c.Value
	if os.Type != nil && cs.Type != nil && fmt.Sprint(os.Type.Slice()) != fmt.Sprint(cs.Type.Slice()) {
		out = append(out, fmt.Sprintf("%s: type %v became %v", where, os.Type.Slice(), cs.Type.Slice()))
	}
	if os.Format != cs.Format && os.Format != "" {
		out = append(out, fmt.Sprintf("%s: format %q became %q", where, os.Format, cs.Format))
	}
	have := map[string]bool{}
	for _, v := range cs.Enum {
		have[fmt.Sprint(v)] = true
	}
	for _, v := range os.Enum {
		if len(cs.Enum) > 0 && !have[fmt.Sprint(v)] {
			out = append(out, fmt.Sprintf("%s: enum value %v removed", where, v))
		}
	}
	for name, p := range os.Properties {
		cp, ok := cs.Properties[name]
		if !ok {
			out = append(out, fmt.Sprintf("%s: field %s removed", where, name))
			continue
		}
		out = append(out, diffSchema(where+"."+name, p, cp, seen)...)
	}
	out = append(out, diffSchema(where+"[]", os.Items, cs.Items, seen)...)
	for i, s := range os.AllOf {
		if i < len(cs.AllOf) {
			out = append(out, diffSchema(fmt.Sprintf("%s.allOf[%d]", where, i), s, cs.AllOf[i], seen)...)
		}
	}
	return out
}

const freezeBase = `openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /v1/things:
    get:
      operationId: things.list
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  name: {type: string}
                  kind: {type: string, enum: [a, b]}
                  tags: {type: array, items: {type: object, properties: {id: {type: string}}}}
  /v1/draft:
    get:
      operationId: draft.get
      x-stability: preview
      responses:
        "200": {description: ok}
`

func parseFreeze(t *testing.T, s string) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestBreakingChangesDetected(t *testing.T) {
	old := parseFreeze(t, freezeBase)
	cases := []struct {
		name, from, to, want string
	}{
		{"additive is fine", "name: {type: string}", "name: {type: string}\n                  extra: {type: integer}", ""},
		{"removed field", "name: {type: string}", "renamed: {type: string}", "field name removed"},
		{"type change", "name: {type: string}", "name: {type: integer}", "type [string] became [integer]"},
		{"enum removal", "enum: [a, b]", "enum: [a]", "enum value b removed"},
		{"enum addition is fine", "enum: [a, b]", "enum: [a, b, c]", ""},
		{"nested field removed", "id: {type: string}", "other: {type: string}", "field id removed"},
		{"removed operation", "operationId: things.list", "operationId: things.list\n      x-moved: true", ""},
	}
	for _, c := range cases {
		cur := parseFreeze(t, strings.Replace(freezeBase, c.from, c.to, 1))
		got := breakingChanges(old, cur)
		if c.want == "" && len(got) != 0 || c.want != "" && !containsSub(got, c.want) {
			t.Errorf("%s: got %v, want %q", c.name, got, c.want)
		}
	}
	removed := parseFreeze(t, `openapi: 3.0.3
info: {title: t, version: "1"}
paths: {}
`)
	if got := breakingChanges(old, removed); len(got) != 1 || !containsSub(got, "GET /v1/things: operation removed") {
		t.Errorf("removed op (preview ops exempt): got %v", got)
	}
}

func containsSub(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
