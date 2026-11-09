// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tr is the Turkish pack of OpenBekci (milestone TR-1): the Law 634
// (Kat Mülkiyeti Kanunu) legal presets, Turkish document templates, Turkish
// account names, il/ilçe reference data and public holidays, the vergi
// numarası / TR IBAN / plate validators, the KVKK consent texts, the labels of
// everything it adds and the settings schema.
//
// The presets only fill site settings with defaults; every value is editable
// by the site where its yönetim planı allows. Every legal value is cited in
// LEGAL.md. The payment, SMS and bank-statement contributions of milestone
// TR-2 (iyzico, İleti Merkezi, bank formats) are declared in the settings
// schema and implemented separately.
package tr

import (
	"context"

	"github.com/openbekci/openbekci/sdk"
)

// ID is the pack id; everything the pack contributes is prefixed with it.
const ID = "tr"

// Version is the pack's own version.
const Version = "0.1.0"

// Pack is the Turkish pack. New returns a ready one.
type Pack struct{}

// New returns the Turkish pack.
func New() *Pack { return &Pack{} }

var _ sdk.Pack = (*Pack)(nil)

// Manifest implements sdk.Pack.
func (*Pack) Manifest() sdk.Manifest {
	return sdk.Manifest{ID: ID, Version: Version, MinSDK: sdk.Version, Countries: []string{"TR"}}
}

// Register implements sdk.Pack: one call per extension point.
func (*Pack) Register(_ context.Context, r sdk.Registry) error {
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
