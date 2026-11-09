// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tr is the Turkish pack of OpenBekci (milestone TR-1): the Law 634
// (Kat Mülkiyeti Kanunu) legal presets, Turkish document templates, Turkish
// account names, il/ilçe reference data and public holidays, the vergi
// numarası / TR IBAN / plate validators, the KVKK consent texts, the labels of
// everything it adds and the settings schema.
//
// The presets only fill site settings with defaults; every value is editable
// by the site where its yönetim planı allows. Every legal value is cited in
// LEGAL.md.
//
// Milestone TR-2 adds the iyzico payment provider (PAYMENTS.md), the İleti
// Merkezi SMS provider and the statement importers of Ziraat Bankası and İş
// Bankası (BANKS.md), plus the reference-code scheme helpers.
package tr

import (
	"context"
	"net/http"

	"github.com/openbekci/openbekci/sdk"
)

// ID is the pack id; everything the pack contributes is prefixed with it.
const ID = "tr"

// Version is the pack's own version.
const Version = "0.1.0"

// Pack is the Turkish pack. New returns a ready one.
type Pack struct {
	svc        sdk.Services
	http       *http.Client
	smsTestURL string
}

// New returns the Turkish pack.
func New(opts ...Option) *Pack {
	p := &Pack{}
	for _, o := range opts {
		o(p)
	}
	return p
}

var _ sdk.Pack = (*Pack)(nil)

// Manifest implements sdk.Pack.
func (*Pack) Manifest() sdk.Manifest {
	return sdk.Manifest{ID: ID, Version: Version, MinSDK: sdk.Version, Countries: []string{"TR"}}
}

// Register implements sdk.Pack: one call per extension point.
func (p *Pack) Register(_ context.Context, r sdk.Registry) error {
	p.svc = r.Services()
	r.RegisterPaymentProvider(&iyzico{p: p})
	r.RegisterSMSProvider(&iletiMerkezi{p: p})
	formats, err := loadBankFormats()
	if err != nil {
		return err
	}
	for _, f := range formats {
		r.RegisterStatementImporter(&bankImporter{p: p, def: f})
	}
	rules := Rules{}
	r.RegisterQuorumPreset(rules)
	r.RegisterThresholdPreset(rules)
	r.RegisterVoteWeightPreset(rules)
	r.RegisterSchedulePreset(rules)
	r.RegisterTransferRule(rules)
	r.RegisterLateFeePreset(rules)

	for _, t := range Templates() {
		r.RegisterDocumentTemplate(t)
	}
	r.RegisterChartOfAccounts(chart{})
	r.RegisterAddressFormat(address{})
	r.RegisterValidator(taxID{})
	r.RegisterValidator(iban{})
	r.RegisterValidator(plate{})
	r.RegisterReferenceData(areas{})
	for _, c := range consents() {
		r.RegisterConsentTexts(c)
	}
	r.RegisterCatalog(labels{})
	r.RegisterSettingsSchema(settings{})
	return nil
}
