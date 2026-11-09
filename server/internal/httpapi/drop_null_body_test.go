package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// null behind more than the 64 peeked bytes of white space is still an absent body.
func TestDropNullBodyAfterLongWhitespace(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat(" ", 100)+"null"))
	dropNullBody(r)
	if b, _ := io.ReadAll(r.Body); len(b) != 0 {
		t.Fatalf("body %q kept", b)
	}
}

// an oversized body on an optional-body route still meets the size limit.
func TestDropNullBodyKeepsSizeError(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`{"a":"`+strings.Repeat("x", 300)+`"}`))
	r.Body = http.MaxBytesReader(w, r.Body, 100)
	dropNullBody(r)
	_, err := io.ReadAll(r.Body)
	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) {
		t.Fatalf("read error %v, want the size limit", err)
	}
}
