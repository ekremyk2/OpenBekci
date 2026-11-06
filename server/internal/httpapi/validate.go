package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmw "github.com/oapi-codegen/nethttp-middleware"

	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// requestValidator returns the kin-openapi request validation middleware with
// errors rendered as problem+json.
func requestValidator(spec *openapi3.T) func(http.Handler) http.Handler {
	return nethttpmw.OapiRequestValidatorWithOptions(spec, &nethttpmw.Options{
		Options: openapi3filter.Options{
			MultiError: true,
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

// validationProblem converts a kin-openapi validation error to a problem.
func validationProblem(err error, suggested int) *problem.Error {
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
				return problem.New(http.StatusMethodNotAllowed, problem.CodeBadRequest, "method not allowed")
			}
			return problem.Wrap(http.StatusInternalServerError, problem.CodeInternalError, "", e)
		}
		fs, bad, detail := requestErrorFields(re)
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
			return nil, true, "unsupported Content-Type"
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
