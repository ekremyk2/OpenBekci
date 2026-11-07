package notifications

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"unicode/utf8"
)

//go:embed locales/*.json
var localeFS embed.FS

// DefaultLocale is used when neither the recipient nor the organization names
// a supported language.
const DefaultLocale = "en"

type templateSet struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Subject string `json:"subject"`
	SMS     string `json:"sms"`
}

// Rendered is a message in one language, ready for every channel.
type Rendered struct {
	Title   string // in-app title, push title
	Body    string // in-app body, email body
	Subject string // email subject
	SMS     string // text message
}

type catalog map[string]map[string]*template.Template // locale -> "type/part" -> template

var catalogs = mustLoad()

func mustLoad() map[string]catalog {
	out := map[string]catalog{}
	for _, loc := range []string{"en", "tr"} {
		raw, err := localeFS.ReadFile("locales/" + loc + ".json")
		if err != nil {
			panic(err)
		}
		var sets map[string]templateSet
		if err := json.Unmarshal(raw, &sets); err != nil {
			panic(fmt.Errorf("locales/%s.json: %w", loc, err))
		}
		c := catalog{loc: {}}
		for typ, set := range sets {
			for part, text := range map[string]string{"title": set.Title, "body": set.Body, "subject": set.Subject, "sms": set.SMS} {
				t, err := template.New(typ + "/" + part).Option("missingkey=zero").Parse(text)
				if err != nil {
					panic(fmt.Errorf("locales/%s.json %s %s: %w", loc, typ, part, err))
				}
				c[loc][typ+"/"+part] = t
			}
		}
		out[loc] = c
	}
	return out
}

// NormalizeLocale maps a BCP 47 tag to a supported locale ("tr-TR" gives "tr").
func NormalizeLocale(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	if _, ok := catalogs[l]; ok {
		return l
	}
	return ""
}

// Render fills the templates of typ in locale with payload. An unknown type
// renders the type name as title; a missing payload key renders empty.
func Render(typ, locale string, payload map[string]any) Rendered {
	loc := NormalizeLocale(locale)
	if loc == "" {
		loc = DefaultLocale
	}
	c := catalogs[loc][loc]
	if c == nil {
		return Rendered{Title: typ}
	}
	part := func(name string) string {
		t, ok := c[typ+"/"+name]
		if !ok {
			if name == "title" {
				return typ
			}
			return ""
		}
		var b bytes.Buffer
		if err := t.Execute(&b, payload); err != nil {
			return ""
		}
		return strings.TrimSpace(strings.ReplaceAll(b.String(), "<no value>", ""))
	}
	r := Rendered{Title: part("title"), Body: part("body"), Subject: part("subject"), SMS: part("sms")}
	if r.Subject == "" {
		r.Subject = r.Title
	}
	if r.SMS == "" {
		r.SMS = r.Title
	}
	return r
}

// gsm7 is the GSM 03.38 basic character set; ext characters take two septets.
const gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
const gsm7Ext = "^{}\\[~]|€"

// SMSSegments is how many billed segments text takes: 160 (153 when split)
// GSM 7-bit septets, or 70 (67) UCS-2 characters when any character is outside
// the GSM alphabet (the Turkish ş, ğ, ı and İ are).
func SMSSegments(text string) int {
	if text == "" {
		return 0
	}
	septets := 0
	gsm := true
	for _, r := range text {
		switch {
		case strings.ContainsRune(gsm7Basic, r):
			septets++
		case strings.ContainsRune(gsm7Ext, r):
			septets += 2
		default:
			gsm = false
		}
	}
	if gsm {
		if septets <= 160 {
			return 1
		}
		return (septets + 152) / 153
	}
	n := utf8.RuneCountInString(text)
	for _, r := range text {
		if r > 0xFFFF {
			n++ // surrogate pair
		}
	}
	if n <= 70 {
		return 1
	}
	return (n + 66) / 67
}
