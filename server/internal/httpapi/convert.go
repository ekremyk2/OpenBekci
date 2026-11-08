package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoPeople = errors.New("people service not configured")

func (s *apiServer) tn() (*tenancy.Service, error) {
	if s.tenancy == nil {
		return nil, problem.Internal(errNoTenancy)
	}
	return s.tenancy, nil
}

func (s *apiServer) pp() (*people.Service, error) {
	if s.people == nil {
		return nil, problem.Internal(errNoPeople)
	}
	return s.people, nil
}

// conv converts a service value to the generated response type through JSON:
// the services' JSON tags are the spec's property names.
func conv[T any](v any) (T, error) {
	var out T
	raw, err := json.Marshal(v)
	if err != nil {
		return out, problem.Internal(fmt.Errorf("encode response: %w", err))
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, problem.Internal(fmt.Errorf("decode response: %w", err))
	}
	return out, nil
}

// listOf builds a generated list type {items, next_cursor}.
func listOf[T any](items any, next string) (T, error) {
	var nc any
	if next != "" {
		nc = next
	}
	return conv[T](map[string]any{"items": items, "next_cursor": nc})
}

// docOf turns a generated request body into the fields the client sent.
func docOf(body any) (tenancy.Doc, error) {
	d, err := tenancy.ToDoc(body)
	if err != nil {
		return nil, problem.BadRequest("the request body could not be read")
	}
	return d, nil
}

func hasBody(isNil bool) error {
	if isNil {
		return problem.BadRequest("the request body is missing")
	}
	return nil
}

func pageOf[L ~int, C ~string](l *L, c *C) tenancy.PageParams {
	var p tenancy.PageParams
	if l != nil {
		p.Limit = int(*l)
	}
	if c != nil {
		p.Cursor = string(*c)
	}
	return p
}

// ifMatchVersion reads the If-Match header (the resource's version, optionally quoted).
func ifMatchVersion(h *string) (int, error) {
	if h == nil {
		return 0, problem.Validation(problem.Field{Field: "If-Match", Code: "required"})
	}
	v, err := strconv.Atoi(strings.Trim(*h, `"`))
	if err != nil {
		return 0, problem.Validation(problem.Field{Field: "If-Match", Code: "invalid_format"})
	}
	return v, nil
}

// MaxUploadBytes is the size limit of an uploaded file (spreadsheet or archive).
const MaxUploadBytes = 32 << 20

type upload struct {
	filename string
	data     []byte
	fields   map[string]string
}

// readUpload reads a multipart body: the part "file" (bounded) and small text fields.
func readUpload(mr *multipart.Reader) (upload, error) {
	if mr == nil {
		return upload{}, problem.BadRequest("the request body is missing")
	}
	u := upload{fields: map[string]string{}}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return u, bodyReadError(err)
		}
		name := part.FormName()
		var limit int64 = 4096
		if name == "file" || name == "archive" {
			limit = MaxUploadBytes
		}
		var buf bytes.Buffer
		n, err := io.Copy(&buf, io.LimitReader(part, limit+1))
		if err != nil {
			return u, bodyReadError(err)
		}
		if n > limit {
			return u, tooLarge(limit, nil)
		}
		if name == "file" || name == "archive" {
			u.filename, u.data = part.FileName(), buf.Bytes()
		} else {
			u.fields[name] = buf.String()
		}
	}
	if u.data == nil {
		return u, problem.Validation(problem.Field{Field: "file", Code: "required"})
	}
	return u, nil
}
