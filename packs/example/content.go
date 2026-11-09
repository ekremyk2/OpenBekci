// SPDX-License-Identifier: AGPL-3.0-or-later

package example

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

// ---- SMS

type smsProvider struct{ p *Pack }

func (*smsProvider) ID() string { return ID }

func (s *smsProvider) Send(_ context.Context, to, text string) (string, error) {
	if !strings.HasPrefix(to, "+") {
		return "", fmt.Errorf("%q is not an E.164 number: %w", to, sdk.ErrRejected)
	}
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	s.p.sent = append(s.p.sent, SMS{To: to, Text: text})
	return fmt.Sprintf("msg-%d", len(s.p.sent)), nil
}

// ---- legal presets: values for existing site settings, not law

type presets struct{}

func (presets) ID() string { return ID }

func (presets) Quorum(context.Context, sdk.SiteInfo) (sdk.QuorumDefaults, error) {
	return sdk.QuorumDefaults{
		FirstCall:  sdk.QuorumRule{Basis: "units", Numerator: 1, Denominator: 2},
		SecondCall: sdk.QuorumRule{Basis: "none"},
	}, nil
}

func (presets) Thresholds(context.Context, sdk.SiteInfo) (sdk.ThresholdDefaults, error) {
	return sdk.ThresholdDefaults{Default: sdk.Threshold{Of: "present", Numerator: 1, Denominator: 2},
		ByTopic: map[string]sdk.Threshold{"budget": {Of: "all", Numerator: 2, Denominator: 3}}}, nil
}

func (presets) VoteWeight(context.Context, sdk.SiteInfo) (sdk.VoteWeightDefaults, error) {
	return sdk.VoteWeightDefaults{Weight: "per_unit", CoOwnerRepresentation: "single_representative", ProxyCapPerPerson: 2}, nil
}

func (presets) Schedule(context.Context, sdk.SiteInfo) (sdk.ScheduleDefaults, error) {
	return sdk.ScheduleDefaults{MinNoticeDays: 14, SecondMeetingMinDay: 7, SecondMeetingMaxDay: 14, OrdinaryAssemblyMonth: 3}, nil
}

func (presets) Transfer(context.Context, sdk.SiteInfo) (sdk.TransferDefaults, error) {
	return sdk.TransferDefaults{DebtOnMoveOut: "stays_with_person"}, nil
}

func (presets) LateFee(context.Context, sdk.SiteInfo) (sdk.LateFeeDefaults, error) {
	ceil := 300
	return sdk.LateFeeDefaults{MonthlyRateCeilingBps: &ceil, DefaultMonthlyRateBps: 200, GraceDays: 5, ProRata: true}, nil
}

// ---- billing policies

// evenPolicy splits a charge evenly; the remainder goes to the first units.
type evenPolicy struct{}

func (evenPolicy) ID() string { return ID + ".even" }

func (evenPolicy) Allocate(_ context.Context, req sdk.AllocationRequest) ([]sdk.UnitAmount, error) {
	n := int64(len(req.Units))
	if n == 0 {
		return nil, fmt.Errorf("no units: %w", sdk.ErrRejected)
	}
	out := make([]sdk.UnitAmount, n)
	base, rem := req.Total.Amount/n, req.Total.Amount%n
	for i, u := range req.Units {
		out[i] = sdk.UnitAmount{UnitID: u.UnitID, Amount: base}
		if int64(i) < rem {
			out[i].Amount++
		}
	}
	return out, nil
}

// newestFirst settles the newest open item first, fees before principal.
type newestFirst struct{}

func (newestFirst) ID() string { return ID + ".newest_first" }

func (newestFirst) Order(_ context.Context, items []sdk.OpenItem) ([]sdk.ID, error) {
	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(a, b sdk.OpenItem) int {
		switch {
		case a.DueOn.After(b.DueOn):
			return -1
		case a.DueOn.Before(b.DueOn):
			return 1
		case a.Kind == "fee" && b.Kind != "fee":
			return -1
		case b.Kind == "fee" && a.Kind != "fee":
			return 1
		}
		return 0
	})
	ids := make([]sdk.ID, len(sorted))
	for i, it := range sorted {
		ids[i] = it.ID
	}
	return ids, nil
}

// ---- documents and localization

type receiptTemplate struct{}

func (receiptTemplate) ID() string        { return ID + ".receipt" }
func (receiptTemplate) Kind() string      { return "receipt" }
func (receiptTemplate) Locales() []string { return []string{"en", "tr"} }

func (receiptTemplate) Render(_ context.Context, req sdk.RenderRequest) (sdk.Document, error) {
	title := "Receipt"
	if req.Locale == "tr" {
		title = "Makbuz"
	}
	body := fmt.Sprintf("%s\nNo: %v\nAmount: %v\n", title, req.Data["receipt_no"], req.Data["amount"])
	return sdk.Document{Filename: "receipt.txt", MIME: "text/plain; charset=utf-8", Body: []byte(body)}, nil
}

type chart struct{}

func (chart) ID() string { return ID }

func (chart) Names(locale string) map[string]string {
	if locale == "tr" {
		return map[string]string{"1100": "Banka (example)", "4000": "Aidat (example)"}
	}
	return map[string]string{"1100": "Bank (example)", "4000": "Dues (example)"}
}

type address struct{}

func (address) ID() string      { return ID + ".xx" }
func (address) Country() string { return "XX" }

func (address) Fields() []sdk.AddressField {
	return []sdk.AddressField{
		{Key: "line1", LabelKey: ID + ".address.line1", Required: true},
		{Key: "province", LabelKey: ID + ".address.province", Required: true, Reference: "province"},
		{Key: "postal_code", LabelKey: ID + ".address.postal_code", Pattern: `^[0-9]{5}$`},
	}
}

// taxID accepts eight digits; a real validator checks a checksum.
type taxID struct{}

func (taxID) ID() string      { return ID + ".tax_id" }
func (taxID) Kind() string    { return "tax_id" }
func (taxID) Country() string { return "XX" }

func (taxID) Validate(v string) error {
	if len(v) != 8 || strings.Trim(v, "0123456789") != "" {
		return fmt.Errorf("%q is not eight digits", v)
	}
	return nil
}

type areas struct{}

func (areas) ID() string      { return ID + ".areas" }
func (areas) Kinds() []string { return []string{"province"} }

func (areas) List(_ context.Context, kind, parent string) ([]sdk.ReferenceItem, error) {
	if kind != "province" || parent != "" {
		return nil, nil
	}
	return []sdk.ReferenceItem{{Code: "n", Name: "North"}, {Code: "s", Name: "South"}}, nil
}

type privacy struct{}

func (privacy) ID() string { return ID + ".privacy" }

func (privacy) Text(locale string) (sdk.ConsentText, bool) {
	switch locale {
	case "en":
		return sdk.ConsentText{Version: "1", Markdown: "# Privacy\nThis is an example notice."}, true
	case "tr":
		return sdk.ConsentText{Version: "1", Markdown: "# Gizlilik\nBu bir ornek metindir."}, true
	}
	return sdk.ConsentText{}, false
}

type labels struct{}

func (labels) ID() string { return ID + ".labels" }

func (labels) Labels(locale string) map[string]string {
	if locale == "tr" {
		return map[string]string{ID + ".status.paid": "Odendi", ID + ".status.pending": "Bekliyor"}
	}
	return map[string]string{ID + ".status.paid": "Paid", ID + ".status.pending": "Pending"}
}

// ---- settings

// settings is the pack's settings schema: JSON Schema with x-ui and x-secret.
// Property names carry the pack id as a prefix.
type settings struct{}

var orgSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["example_api_key", "example_webhook_secret"],
  "properties": {
    "example_api_key":        {"type": "string", "minLength": 1, "x-secret": true, "x-ui": {"order": 1, "widget": "password", "help": "example.help.api_key"}},
    "example_webhook_secret": {"type": "string", "minLength": 8, "x-secret": true, "x-ui": {"order": 2, "widget": "password"}},
    "example_sender":         {"type": "string", "maxLength": 11, "x-ui": {"order": 3, "group": "sms"}}
  }
}`)

var siteSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "example_account": {"type": "string", "x-ui": {"order": 1}}
  }
}`)

func (settings) Schema(scope sdk.ScopeKind) json.RawMessage {
	switch scope {
	case sdk.ScopeOrg:
		return orgSchema
	case sdk.ScopeSite:
		return siteSchema
	}
	return nil
}
