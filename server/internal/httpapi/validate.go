package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmw "github.com/oapi-codegen/nethttp-middleware"

	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// validation validates the request against the spec. Operations marked
// x-openbekci-stream get their parameters validated but not their body (it is
// streamed, never buffered); the handler checks what the body carries. A
// streamed request must still be multipart, else 415.
func (p *pipeline) validation(next http.Handler) http.Handler {
	full := requestValidator(p.spec, false)(next)
	streamed := requestValidator(p.spec, true)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := p.op(r)
		if !info.stream {
			if info.bodyOptional {
				dropNullBody(r)
			}
			full.ServeHTTP(w, r)
			return
		}
		if info.hasBody && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
			problem.Write(w, r, problem.New(http.StatusUnsupportedMediaType, problem.CodeUnsupportedMediaType, "the request Content-Type must be multipart/form-data"))
			return
		}
		streamed.ServeHTTP(w, r)
	})
}

// requestValidator returns the kin-openapi request validation middleware with
// errors rendered as problem+json. With excludeBody only parameters are checked.
func requestValidator(spec *openapi3.T, excludeBody bool) func(http.Handler) http.Handler {
	return nethttpmw.OapiRequestValidatorWithOptions(spec, &nethttpmw.Options{
		Options: openapi3filter.Options{
			ExcludeRequestBody: excludeBody,
			MultiError:         true,
			// Authentication is enforced by the pipeline's authentication step
			// (Authenticator), not by kin; here only the parameters and body count.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		SilenceServersWarning: true,
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmw.ErrorHandlerOpts) {
			problem.Write(w, r, validationProblem(err, opts.StatusCode))
		},
	})
}

// dropNullBody treats a request body that is exactly JSON null (or only
// whitespace) as absent, for operations whose requestBody is not required:
// generated clients send null for an omitted optional body. Required bodies
// never pass through here.
func dropNullBody(r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	const peek = 4096 // shortcut: null behind more than 4 KiB of white space reaches the validator as is, if a client ever does that read to EOF
	buf := make([]byte, peek)
	n, err := io.ReadFull(r.Body, buf)
	buf = buf[:n]
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		// a read error (for example the size limit): the validator meets it again
		r.Body = readCloser{io.MultiReader(bytes.NewReader(buf), errReader{err}), r.Body}
		return
	}
	if err != nil { // the whole body fit in the peek
		if t := bytes.TrimSpace(buf); len(t) == 0 || string(t) == "null" {
			_ = r.Body.Close()
			r.Body, r.ContentLength = http.NoBody, 0
			return
		}
	}
	r.Body = readCloser{io.MultiReader(bytes.NewReader(buf), r.Body), r.Body}
}

type readCloser struct {
	io.Reader
	io.Closer
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// validationProblem converts a kin-openapi validation error to a problem.
func validationProblem(err error, suggested int) *problem.Error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return tooLarge(mbe.Limit, err)
	}
	var fields []problem.Field
	var firstDetail string
	malformed := false
	for _, e := range flatten(err) {
		var re *openapi3filter.RequestError
		if !errors.As(e, &re) {
			if suggested == http.StatusNotFound {
				return problem.NotFound("no such resource")
			}
			if suggested == http.StatusMethodNotAllowed {
				return problem.New(http.StatusMethodNotAllowed, problem.CodeMethodNotAllowed, "method not allowed")
			}
			return problem.Wrap(http.StatusInternalServerError, problem.CodeInternalError, "", e)
		}
		fs, bad, detail := requestErrorFields(re)
		if detail == detailUnsupportedMediaType {
			return problem.New(http.StatusUnsupportedMediaType, problem.CodeUnsupportedMediaType, "the request Content-Type is not supported for this operation")
		}
		if firstDetail == "" {
			firstDetail = detail
		}
		malformed = malformed || bad
		fields = append(fields, fs...)
	}
	if malformed && len(fields) == 0 {
		return problem.BadRequest(firstDetail)
	}
	if len(fields) == 0 {
		return problem.BadRequest(firstDetail)
	}
	return problem.Validation(fields...)
}

func flatten(err error) []error {
	// A type assertion, not errors.As: a RequestError unwraps to its inner
	// MultiError, and flattening through it would lose the parameter or body.
	if me, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // see above
		var out []error
		for _, e := range me {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return []error{err}
}

var missingProp = regexp.MustCompile(`property "([^"]+)" is missing`)

const detailUnsupportedMediaType = "unsupported Content-Type"

// requestErrorFields describes one RequestError. malformed is true when the
// request could not be parsed at all (not a per-field problem).
func requestErrorFields(re *openapi3filter.RequestError) (fields []problem.Field, malformed bool, detail string) {
	if re.Parameter != nil {
		name := re.Parameter.Name
		switch {
		case errors.Is(re.Err, openapi3filter.ErrInvalidRequired), errors.Is(re.Err, openapi3filter.ErrInvalidEmptyValue):
			return []problem.Field{{Field: name, Code: "required"}}, false, ""
		}
		var pe *openapi3filter.ParseError
		if errors.As(re.Err, &pe) {
			return []problem.Field{{Field: name, Code: "invalid_format"}}, false, ""
		}
		var all []problem.Field
		for _, se := range schemaErrors(re.Err) {
			all = append(all, schemaField(name, se))
		}
		if len(all) > 0 {
			return all, false, ""
		}
		return []problem.Field{{Field: name, Code: "invalid"}}, false, ""
	}

	if re.RequestBody != nil {
		if errors.Is(re.Err, openapi3filter.ErrInvalidRequired) {
			return []problem.Field{{Field: "body", Code: "required"}}, false, ""
		}
		if strings.HasPrefix(re.Reason, "header Content-Type") {
			return nil, true, detailUnsupportedMediaType
		}
		ses := schemaErrors(re.Err)
		if len(ses) == 0 {
			// Not valid JSON, or not decodable at all.
			return nil, true, "the request body is not valid JSON"
		}
		for _, se := range ses {
			fields = append(fields, schemaField("", se))
		}
		return fields, false, ""
	}
	return nil, true, re.Reason
}

// schemaErrors collects the leaf *openapi3.SchemaError values of err.
func schemaErrors(err error) []*openapi3.SchemaError {
	var out []*openapi3.SchemaError
	for _, e := range flatten(err) {
		var se *openapi3.SchemaError
		if errors.As(e, &se) {
			out = append(out, se)
		}
	}
	return out
}

// schemaField maps a schema violation to an errors[] entry. base is the
// parameter name for parameters and empty for request bodies.
func schemaField(base string, se *openapi3.SchemaError) problem.Field {
	path := se.JSONPointer()
	parts := make([]string, 0, len(path)+1)
	if base != "" {
		parts = append(parts, base)
	}
	parts = append(parts, path...)
	f := problem.Field{Code: "invalid"}
	switch se.SchemaField {
	case "required":
		f.Code = "required"
		if m := missingProp.FindStringSubmatch(se.Reason); m != nil && (len(parts) == 0 || parts[len(parts)-1] != m[1]) {
			parts = append(parts, m[1])
		}
	case "type":
		f.Code = "invalid_type"
	case "format", "pattern":
		f.Code = "invalid_format"
	case "enum":
		f.Code = "invalid_value"
	case "minLength":
		f.Code, f.Params = "too_short", limit("min", se.Schema.MinLength)
	case "maxLength":
		f.Code = "too_long"
		if se.Schema.MaxLength != nil {
			f.Params = limit("max", *se.Schema.MaxLength)
		}
	case "minimum":
		f.Code = "too_small"
		if se.Schema.Min != nil {
			f.Params = limit("min", *se.Schema.Min)
		}
	case "maximum":
		f.Code = "too_large"
		if se.Schema.Max != nil {
			f.Params = limit("max", *se.Schema.Max)
		}
	case "additionalProperties":
		f.Code = "unknown_field"
	}
	f.Field = strings.Join(parts, ".")
	if f.Field == "" {
		f.Field = "body"
	}
	return f
}

func limit(name string, v any) map[string]any { return map[string]any{name: v} }

// requestBindError maps errors from the generated parameter binding and body
// decoding (strict server RequestErrorHandlerFunc) to problems. With the
// validation middleware in front these are rare.
func requestBindError(err error) *problem.Error {
	var ip *gen.InvalidParamFormatError
	var rp *gen.RequiredParamError
	var rh *gen.RequiredHeaderError
	var tp *gen.TooManyValuesForParamError
	switch {
	case errors.As(err, &ip):
		return problem.Validation(problem.Field{Field: ip.ParamName, Code: "invalid_format"})
	case errors.As(err, &rp):
		return problem.Validation(problem.Field{Field: rp.ParamName, Code: "required"})
	case errors.As(err, &rh):
		return problem.Validation(problem.Field{Field: rh.ParamName, Code: "required"})
	case errors.As(err, &tp):
		return problem.Validation(problem.Field{Field: tp.ParamName, Code: "too_many_values"})
	}
	return problem.Wrap(http.StatusBadRequest, problem.CodeBadRequest, "the request could not be decoded", fmt.Errorf("bind: %w", err))
}
