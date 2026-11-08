package packs

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
)

// The selection points of the content contributions (documents, consent texts,
// reference data, address formats): the core calls them for an organization and
// gets what its enabled packs provide.

// Render implements doctpl.Resolver: it runs the first template of the given
// kind that an enabled pack offers for the locale (the base language of the
// locale is compared: "tr-TR" matches "tr"). ok is false when there is none.
func (h *Host) Render(ctx context.Context, org uuid.UUID, kind, locale string, data map[string]any) (doctpl.Doc, bool, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return doctpl.Doc{}, false, err
	}
	lang := strings.ToLower(locale)
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	for _, pk := range packs {
		for _, t := range pk.C.Templates {
			if t.Kind() != kind || !slices.Contains(t.Locales(), lang) {
				continue
			}
			var doc sdk.Document
			err := h.call(ctx, org, pk.Manifest.ID, "DocumentTemplate.Render", func(ctx context.Context) (err error) {
				doc, err = t.Render(ctx, sdk.RenderRequest{Locale: lang, Data: data})
				return err
			})
			if err != nil {
				return doctpl.Doc{}, true, err
			}
			return doctpl.Doc{MIME: doc.MIME, Body: doc.Body}, true, nil
		}
	}
	return doctpl.Doc{}, false, nil
}

var _ doctpl.Resolver = (*Host)(nil)

// ConsentItem is one consent or privacy text of an enabled pack.
type ConsentItem struct {
	ID       string
	Pack     string
	Version  string
	Locale   string
	Markdown string
}

// Consents returns the consent and privacy texts of the packs enabled for org
// in a locale (the privacy module's consents endpoint).
func (h *Host) Consents(ctx context.Context, org uuid.UUID, locale string) ([]ConsentItem, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []ConsentItem
	for _, pk := range packs {
		for _, c := range pk.C.Consents {
			var txt sdk.ConsentText
			var ok bool
			if err := safe(pk.Manifest.ID, "ConsentTexts.Text", func() error { txt, ok = c.Text(locale); return nil }); err != nil || !ok {
				continue
			}
			out = append(out, ConsentItem{ID: c.ID(), Pack: pk.Manifest.ID, Version: txt.Version, Locale: locale, Markdown: txt.Markdown})
		}
	}
	return out, nil
}

// Reference returns the items of a reference data kind (optionally below a
// parent code) from the packs enabled for org. found is false when no enabled
// pack serves the kind (the endpoint answers 404).
func (h *Host) Reference(ctx context.Context, org uuid.UUID, kind, parent string) (items []sdk.ReferenceItem, found bool, err error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, false, err
	}
	for _, pk := range packs {
		for _, r := range pk.C.ReferenceData {
			if !slices.Contains(r.Kinds(), kind) {
				continue
			}
			found = true
			var got []sdk.ReferenceItem
			err := h.call(ctx, org, pk.Manifest.ID, "ReferenceData.List", func(ctx context.Context) (err error) {
				got, err = r.List(ctx, kind, parent)
				return err
			})
			if err != nil {
				return nil, true, err
			}
			items = append(items, got...)
		}
	}
	return items, found, nil
}

// AddressFormats returns the address formats of the packs enabled for org.
func (h *Host) AddressFormats(ctx context.Context, org uuid.UUID) ([]sdk.AddressFormat, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []sdk.AddressFormat
	for _, pk := range packs {
		out = append(out, pk.C.AddressFormats...)
	}
	return out, nil
}

// ValidateValue runs the validators of a kind that the packs enabled for org
// provide for the country; it returns the first error, or nil. ok is false when
// no validator applies (the caller then uses its own checks).
func (h *Host) ValidateValue(ctx context.Context, org uuid.UUID, kind, country, value string) (ok bool, err error) {
	vs, err := h.Validators(ctx, org, kind)
	if err != nil {
		return false, err
	}
	for _, v := range vs {
		if v.Country() != "" && country != "" && !strings.EqualFold(v.Country(), country) {
			continue
		}
		ok = true
		var verr error
		if perr := safe(OwnerOf(v.ID()), "Validator.Validate", func() error { verr = v.Validate(value); return nil }); perr != nil {
			return true, perr
		}
		if verr != nil {
			return true, verr
		}
	}
	return ok, nil
}
