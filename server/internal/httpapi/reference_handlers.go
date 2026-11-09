package httpapi

import (
	"bytes"
	"context"
	"slices"
	"strings"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// packLabelsSection is the section of the codes answer that carries the label
// catalogs of the packs enabled for the organization (keys "<pack>.<key>").
const packLabelsSection = "pack_labels"

// withPackLabels merges the catalogs of the organization's enabled packs into a
// codes answer: the English labels first, then the answer's locale over them,
// so a key without a translation falls back as the core's labels do.
func (s *apiServer) withPackLabels(ctx context.Context, org gen.OrgId, c codes.Codes) codes.Codes {
	if s.packs == nil {
		return c
	}
	merged := map[string]string{}
	for _, loc := range []string{"en", c.Locale} {
		labels, err := s.packs.Labels(ctx, org, loc)
		if err != nil {
			return c
		}
		for k, v := range labels {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return c
	}
	out := make([]codes.Label, 0, len(merged))
	for k, v := range merged {
		out = append(out, codes.Label{Code: k, Label: v})
	}
	slices.SortFunc(out, func(a, b codes.Label) int { return strings.Compare(a.Code, b.Code) })
	if c.Enums == nil {
		c.Enums = map[string][]codes.Label{}
	}
	c.Enums[packLabelsSection] = out
	return c
}

// CodesListReference serves the reference data of the packs enabled for the organization.
func (s *apiServer) CodesListReference(ctx context.Context, req gen.CodesListReferenceRequestObject) (gen.CodesListReferenceResponseObject, error) {
	if err := authz.RequireInOrg(ctx, codes.PermRead, req.OrgId); err != nil {
		return nil, err
	}
	h, err := s.pk()
	if err != nil {
		return nil, err
	}
	out := gen.ReferenceList{Kind: req.Kind, Items: []gen.ReferenceItem{}}
	if req.Kind == "address_format" {
		formats, err := h.AddressFormats(ctx, req.OrgId)
		if err != nil {
			return nil, err
		}
		if len(formats) == 0 {
			return nil, problem.NotFound("no enabled pack serves address formats")
		}
		fields := []gen.AddressField{}
		for _, f := range formats[0].Fields() {
			af := gen.AddressField{Key: f.Key, LabelKey: f.LabelKey, Required: f.Required}
			if f.Pattern != "" {
				p := f.Pattern
				af.Pattern = &p
			}
			if f.Reference != "" {
				r := f.Reference
				af.Reference = &r
			}
			fields = append(fields, af)
		}
		out.AddressFormat = &fields
		return gen.CodesListReference200JSONResponse(out), nil
	}
	items, found, err := h.Reference(ctx, req.OrgId, req.Kind, deref(req.Params.Parent))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, problem.NotFound("no enabled pack serves reference data of this kind")
	}
	for _, it := range items {
		ri := gen.ReferenceItem{Code: it.Code, Name: it.Name}
		if it.Parent != "" {
			p := it.Parent
			ri.Parent = &p
		}
		out.Items = append(out.Items, ri)
	}
	return gen.CodesListReference200JSONResponse(out), nil
}

// genericPrivacyNotice is the privacy notice of an organization without a
// pack: a short, country-neutral text the organization is expected to replace.
var genericPrivacyNotice = map[string]string{
	"en": "# Privacy notice\n\nThe site management processes your name, contact details, unit and payment records to run the building: to keep the owner and tenant records, send notices and receipts, collect dues and record meetings. Data is shared only with providers that deliver messages and payments for the site, and where the law requires it. Debtor lists are never shown to other residents. You may ask the site management for access to, correction or deletion of your data.\n",
	"tr": "# Gizlilik bildirimi\n\nSite yönetimi; adınızı, iletişim bilgilerinizi, bağımsız bölümünüzü ve ödeme kayıtlarınızı binanın yönetimi için işler: malik ve kiracı kayıtlarını tutmak, bildirim ve makbuz göndermek, aidat toplamak ve toplantıları kaydetmek. Veriler yalnızca site için ileti ve ödeme hizmeti veren sağlayıcılarla ve yasaların gerektirdiği hallerde paylaşılır. Borçlu listeleri diğer sakinlere gösterilmez. Verilerinize erişim, düzeltme veya silme için site yönetimine başvurabilirsiniz.\n",
}

// CodesListConsents serves the privacy notice and consent texts: the enabled
// packs' when they have any in the locale, else the generic notice.
func (s *apiServer) CodesListConsents(ctx context.Context, req gen.CodesListConsentsRequestObject) (gen.CodesListConsentsResponseObject, error) {
	if err := authz.RequireInOrg(ctx, codes.PermRead, req.OrgId); err != nil {
		return nil, err
	}
	locale := strings.ToLower(deref(req.Params.Locale))
	if locale == "" && s.tenancy != nil {
		if org, err := s.tenancy.Get(ctx, req.OrgId); err == nil {
			locale = org.DefaultLocale
		}
	}
	if i := strings.IndexAny(locale, "-_"); i > 0 {
		locale = locale[:i]
	}
	if locale != "tr" {
		locale = "en"
	}
	out := gen.ConsentList{Locale: locale, Items: []gen.Consent{}}
	if s.packs != nil {
		items, err := s.packs.Consents(ctx, req.OrgId, locale)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			pack := it.Pack
			out.Items = append(out.Items, gen.Consent{Id: it.ID, Pack: &pack, Version: it.Version, Locale: it.Locale, Markdown: it.Markdown})
		}
	}
	if len(out.Items) == 0 {
		out.Items = append(out.Items, gen.Consent{Id: "core.privacy_notice", Version: "1", Locale: locale, Markdown: genericPrivacyNotice[locale]})
	}
	return gen.CodesListConsents200JSONResponse(out), nil
}

// GovernanceGetMeetingDocument renders a document only a pack provides.
func (s *apiServer) GovernanceGetMeetingDocument(ctx context.Context, req gen.GovernanceGetMeetingDocumentRequestObject) (gen.GovernanceGetMeetingDocumentResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	pdf, _, err := g.PackDocument(ctx, req.OrgId, req.MeetingId, req.Kind, req.Params.PersonId)
	if err != nil {
		return nil, err
	}
	return gen.GovernanceGetMeetingDocument200ApplicationpdfResponse{Body: bytes.NewReader(pdf), ContentLength: int64(len(pdf))}, nil
}
