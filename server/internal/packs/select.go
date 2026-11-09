package packs

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// The core selects pack implementations only for organizations that have the
// pack enabled. These are the selection points the core calls.

// SMSSettingKey is the organization setting that names the SMS provider
// ("netgsm", or the id of a provider of an enabled pack).
const SMSSettingKey = "sms_provider"

// SMSPicker returns the picker for sms.Routed.
func (h *Host) SMSPicker() sms.Picker { return smsPicker{h} }

type smsPicker struct{ h *Host }

// Pick implements sms.Picker: the organization's sms_provider setting names a
// provider of an enabled pack, or nothing (then the instance gateway sends).
func (p smsPicker) Pick(ctx context.Context, org uuid.UUID) (sms.Gateway, bool) {
	var chosen string
	err := p.h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(settings->>$2, '') FROM organization WHERE id = $1`, org, SMSSettingKey).Scan(&chosen)
	})
	if err != nil || chosen == "" {
		return nil, false
	}
	packs, err := p.h.Active(ctx, org)
	if err != nil {
		return nil, false
	}
	for _, pk := range packs {
		for _, pr := range pk.C.SMSProviders {
			if pr.ID() == chosen {
				return packSMS{h: p.h, org: org, pack: pk.Manifest.ID, p: pr}, true
			}
		}
	}
	return nil, false
}

type packSMS struct {
	h    *Host
	org  uuid.UUID
	pack string
	p    sdk.SMSProvider
}

func (g packSMS) Send(ctx context.Context, to, text string) (id string, err error) {
	err = g.h.call(ctx, g.org, g.pack, "SMS Send", func(ctx context.Context) (err error) { id, err = g.p.Send(ctx, to, text); return err })
	return id, err
}

// SMSProviderChoices lists the SMS providers an organization may name in
// sms_provider: those of its enabled packs.
func (h *Host) SMSProviderChoices(ctx context.Context, org uuid.UUID) ([]string, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, pk := range packs {
		for _, pr := range pk.C.SMSProviders {
			out = append(out, pr.ID())
		}
	}
	return out, nil
}

// ImporterInfo describes a statement importer an enabled pack provides.
type ImporterInfo struct {
	ID      string
	Pack    string
	Formats []string
}

// Importers lists the statement importers of the packs enabled for org
// (the bank import module lists them next to the generic CSV importer).
func (h *Host) Importers(ctx context.Context, org uuid.UUID) ([]ImporterInfo, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []ImporterInfo
	for _, pk := range packs {
		for _, im := range pk.C.Importers {
			out = append(out, ImporterInfo{ID: im.ID(), Pack: pk.Manifest.ID, Formats: im.Formats()})
		}
	}
	return out, nil
}

// ParseStatement parses a statement with a pack importer of an enabled pack.
// ok is false when no enabled pack provides the importer id (the caller then
// falls back to its own importers or reports an unknown importer).
func (h *Host) ParseStatement(ctx context.Context, org uuid.UUID, importerID, filename string, data []byte) (sdk.Statement, bool, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return sdk.Statement{}, false, err
	}
	for _, pk := range packs {
		for _, im := range pk.C.Importers {
			if im.ID() != importerID {
				continue
			}
			var st sdk.Statement
			err := h.call(ctx, org, pk.Manifest.ID, "StatementImporter.Parse", func(ctx context.Context) (err error) {
				st, err = im.Parse(ctx, filename, data)
				return err
			})
			return st, true, err
		}
	}
	return sdk.Statement{}, false, nil
}

// DetectImporter returns the id of the first enabled pack importer that recognizes the file.
func (h *Host) DetectImporter(ctx context.Context, org uuid.UUID, filename string, head []byte) (string, bool, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return "", false, err
	}
	for _, pk := range packs {
		for _, im := range pk.C.Importers {
			var hit bool
			if err := safe(pk.Manifest.ID, "Detect", func() error { hit = im.Detect(filename, head); return nil }); err == nil && hit {
				return im.ID(), true, nil
			}
		}
	}
	return "", false, nil
}

// Labels returns the catalog labels of the packs enabled for org in a locale,
// merged into the core codes endpoint.
func (h *Host) Labels(ctx context.Context, org uuid.UUID, locale string) (map[string]string, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, pk := range packs {
		for _, c := range pk.C.Catalogs {
			_ = safe(pk.Manifest.ID, "Catalog.Labels", func() error {
				for k, v := range c.Labels(locale) {
					out[k] = v
				}
				return nil
			})
		}
	}
	return out, nil
}

// Template finds a document template of the packs enabled for org by id.
func (h *Host) Template(ctx context.Context, org uuid.UUID, id string) (sdk.DocumentTemplate, bool, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, false, err
	}
	for _, pk := range packs {
		for _, t := range pk.C.Templates {
			if t.ID() == id {
				return t, true, nil
			}
		}
	}
	return nil, false, nil
}

// Validators returns the validators of a kind for the packs enabled for org.
func (h *Host) Validators(ctx context.Context, org uuid.UUID, kind string) ([]sdk.Validator, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []sdk.Validator
	for _, pk := range packs {
		for _, v := range pk.C.Validators {
			if v.Kind() == kind {
				out = append(out, v)
			}
		}
	}
	return out, nil
}

// OwnerOf returns the pack id inside a namespaced id ("tr.iyzico" gives "tr").
func OwnerOf(id string) string {
	pack, _, _ := strings.Cut(id, ".")
	return pack
}

func (h *Host) String() string { return fmt.Sprintf("packs.Host(%d packs)", len(h.packs)) }
