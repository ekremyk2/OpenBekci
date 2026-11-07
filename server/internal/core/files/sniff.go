package files

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"unicode"
)

// TypeRule allows one file extension: the content must really look like the
// type (sniffed from its first bytes, never from the client's Content-Type).
type TypeRule struct {
	// Ext is the lower-case extension without the dot.
	Ext string
	// MIME is the type the file is stored and served as.
	MIME string
	// Sniff lists the results of http.DetectContentType (without parameters)
	// the content may have.
	Sniff []string
	// Magic is an alternative: content that starts with these bytes (for
	// formats the standard sniffer calls application/octet-stream).
	Magic []byte
	// Image marks types that are decoded, stripped of metadata by re-encoding
	// and given thumb and medium variants.
	Image bool
}

var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// DefaultTypes is the built-in allowlist: photos, PDFs, office documents and
// plain text. Narrow it with OPENBEKCI_FILES_ALLOWED_EXTENSIONS.
func DefaultTypes() []TypeRule {
	zip := []string{"application/zip"}
	return []TypeRule{
		{Ext: "jpg", MIME: "image/jpeg", Sniff: []string{"image/jpeg"}, Image: true},
		{Ext: "jpeg", MIME: "image/jpeg", Sniff: []string{"image/jpeg"}, Image: true},
		{Ext: "png", MIME: "image/png", Sniff: []string{"image/png"}, Image: true},
		{Ext: "webp", MIME: "image/webp", Sniff: []string{"image/webp"}, Image: true},
		{Ext: "pdf", MIME: "application/pdf", Sniff: []string{"application/pdf"}},
		{Ext: "txt", MIME: "text/plain", Sniff: []string{"text/plain"}},
		{Ext: "csv", MIME: "text/csv", Sniff: []string{"text/plain"}},
		{Ext: "docx", MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Sniff: zip},
		{Ext: "xlsx", MIME: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Sniff: zip},
		{Ext: "pptx", MIME: "application/vnd.openxmlformats-officedocument.presentationml.presentation", Sniff: zip},
		{Ext: "odt", MIME: "application/vnd.oasis.opendocument.text", Sniff: zip},
		{Ext: "ods", MIME: "application/vnd.oasis.opendocument.spreadsheet", Sniff: zip},
		{Ext: "doc", MIME: "application/msword", Magic: oleMagic},
		{Ext: "xls", MIME: "application/vnd.ms-excel", Magic: oleMagic},
	}
}

// FilterTypes keeps the rules whose extension is listed (case-insensitive,
// with or without a leading dot). An empty list keeps every rule.
func FilterTypes(rules []TypeRule, exts []string) []TypeRule {
	if len(exts) == 0 {
		return rules
	}
	want := map[string]bool{}
	for _, e := range exts {
		want[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), ".")] = true
	}
	var out []TypeRule
	for _, r := range rules {
		if want[r.Ext] {
			out = append(out, r)
		}
	}
	return out
}

// sniffHeadLen is how much of the content is looked at (what
// http.DetectContentType uses).
const sniffHeadLen = 512

// match finds the rule for a file named name whose first bytes are head. ok
// is false when the extension is not allowed or the content is not what the
// extension claims.
func match(rules []TypeRule, name string, head []byte) (rule TypeRule, extAllowed, ok bool) {
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	sniffed, _, _ := strings.Cut(http.DetectContentType(head), ";")
	for _, r := range rules {
		if r.Ext != ext {
			continue
		}
		extAllowed = true
		for _, s := range r.Sniff {
			if s == sniffed {
				return r, true, true
			}
		}
		if len(r.Magic) > 0 && bytes.HasPrefix(head, r.Magic) {
			return r, true, true
		}
	}
	return TypeRule{}, extAllowed, false
}

func isImageMIME(m string) bool {
	switch m {
	case "image/jpeg", "image/png", "image/webp":
		return true
	}
	return false
}

// cleanName turns a client-supplied file name into something safe to store
// and echo back in a header: no directories, no control characters, bounded.
func cleanName(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(strings.TrimLeft(s, "."))
	if r := []rune(s); len(r) > 200 {
		// keep the extension when shortening
		ext := path.Ext(s)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		s = string(r[:200-len([]rune(ext))]) + ext
	}
	return s
}
