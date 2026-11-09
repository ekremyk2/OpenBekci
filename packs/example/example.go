// SPDX-License-Identifier: AGPL-3.0-or-later

// Package example is the deliberately tiny example pack: it implements every
// extension point of the OpenBekci pack SDK minimally (a fake payment provider,
// a tiny CSV statement importer, the six legal presets, one validator, one
// template, one setting, one webhook route, one job, one subscriber and one
// migration) so that it can serve as the starting point of a real pack
// (tools/newpack copies it) and as the pack the SDK's test harness runs.
//
// It encodes no law and no real provider. Read it top to bottom: Register is
// the table of contents.
package example

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// ID is the pack id; everything the pack contributes is prefixed with it.
const ID = "example"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Pack is the example pack. New returns a ready one; the zero value is not usable.
type Pack struct {
	svc sdk.Services

	mu   sync.Mutex
	sent []SMS
	chat []Chat
	// Unhealthy makes Ready fail, to show the /readyz contribution.
	Unhealthy bool
}

// SMS is a message the example SMS provider "sent".
type SMS struct{ To, Text string }

// New returns the example pack.
func New() *Pack { return &Pack{} }

// Sent returns the messages the example SMS provider was asked to send.
func (p *Pack) Sent() []SMS {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SMS(nil), p.sent...)
}

var _ sdk.Pack = (*Pack)(nil)

// Manifest implements sdk.Pack.
func (p *Pack) Manifest() sdk.Manifest {
	return sdk.Manifest{ID: ID, Version: "0.1.0", MinSDK: sdk.Version, Countries: []string{"XX"}}
}

// Register implements sdk.Pack: one call per extension point.
func (p *Pack) Register(_ context.Context, r sdk.Registry) error {
	p.svc = r.Services()

	r.RegisterPaymentProvider(&provider{p: p})
	r.RegisterSMSProvider(&smsProvider{p: p})
	// optional extension points (SDK 1.1) are found by type assertion, so the pack still loads on an older host
	if cr, ok := r.(sdk.ChatRegistrar); ok {
		cr.RegisterChatProvider(&chatProvider{p: p})
	}
	r.RegisterStatementImporter(importer{})

	// one value may implement several presets; the registry keeps them apart by kind
	rules := presets{}
	r.RegisterQuorumPreset(rules)
	r.RegisterThresholdPreset(rules)
	r.RegisterVoteWeightPreset(rules)
	r.RegisterSchedulePreset(rules)
	r.RegisterTransferRule(rules)
	r.RegisterLateFeePreset(rules)

	r.RegisterAllocationPolicy(evenPolicy{})
	r.RegisterPaymentAllocationOrder(newestFirst{})
	r.RegisterDocumentTemplate(receiptTemplate{})
	r.RegisterChartOfAccounts(chart{})
	r.RegisterAddressFormat(address{})
	r.RegisterValidator(taxID{})
	r.RegisterReferenceData(areas{})
	r.RegisterConsentTexts(privacy{})
	r.RegisterCatalog(labels{})
	r.RegisterSettingsSchema(settings{})

	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	r.RegisterMigrations(migrations)

	// a callback route: public, the pack verifies the caller
	r.RegisterRoutes(sdk.Public(http.MethodPost, "/callback", p.callback))
	// a background job
	r.RegisterJob(sdk.JobDefinition{Kind: ID + ".cleanup", Handler: p.cleanup})
	// an event subscriber: handlers get ids, never records, and must be idempotent on them
	r.Subscribe(sdk.EventPaymentRecorded, ID+".on_payment", p.onPayment)
	return nil
}

// Optional interfaces, found by the host with type assertions.

var (
	_ sdk.OnEnable         = (*Pack)(nil)
	_ sdk.OnDisable        = (*Pack)(nil)
	_ sdk.ValidateSettings = (*Pack)(nil)
	_ sdk.StartupCheck     = (*Pack)(nil)
	_ sdk.Ready            = (*Pack)(nil)
)

// OnEnable seeds the pack's data for the organization; it must be idempotent.
func (p *Pack) OnEnable(ctx context.Context, org sdk.ID) error {
	return p.mark(ctx, "lifecycle", "enabled:"+string(org), "")
}

// OnDisable runs when the organization disables the pack; data stays.
func (p *Pack) OnDisable(ctx context.Context, org sdk.ID) error {
	return p.mark(ctx, "lifecycle", "disabled:"+string(org)+":"+time.Now().UTC().Format(time.RFC3339Nano), "")
}

// ValidateSettings is the hook for checks a JSON Schema cannot express, such
// as testing credentials against the provider.
func (p *Pack) ValidateSettings(_ context.Context, scope sdk.Scope, values map[string]any) error {
	if scope.Kind == sdk.ScopeOrg {
		if key, _ := values["example_api_key"].(string); key == "bad" {
			return fmt.Errorf("the provider refused the api key: %w", sdk.ErrCredentials)
		}
		if sender, _ := values["example_sender"].(string); sender == "forbidden" {
			return fmt.Errorf("sender %q is not allowed", sender)
		}
	}
	return nil
}

// StartupCheck would verify the pack's own configuration at boot.
func (p *Pack) StartupCheck(context.Context) error { return nil }

// Ready contributes to /readyz (warn-only).
func (p *Pack) Ready(context.Context) error {
	if p.Unhealthy {
		return fmt.Errorf("example provider is unreachable")
	}
	return nil
}

// mark records an idempotent event row in the pack's own table.
func (p *Pack) mark(ctx context.Context, kind, ref, site string) error {
	return p.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		var siteArg any
		if site != "" {
			siteArg = site
		}
		org, _ := sdk.OrgFrom(ctx)
		_, err := tx.Exec(ctx, `INSERT INTO pack_example_event (org_id, site_id, kind, ref) VALUES ($1::uuid, $2::uuid, $3, $4) ON CONFLICT (org_id, kind, ref) DO NOTHING`, string(org), siteArg, kind, ref)
		return err
	})
}

// callback is the example webhook/callback route. Anything a client calls goes
// through a core endpoint; routes exist only for providers calling back.
func (p *Pack) callback(w http.ResponseWriter, r *http.Request) {
	org, _ := sdk.OrgFrom(r.Context())
	secret, _ := sdk.SettingString(r.Context(), p.svc.Settings(), sdk.OrgScope(), "example_webhook_secret")
	body, _ := io.ReadAll(r.Body)
	if secret == "" || !verify(secret, body, r.Header.Get(SignatureHeader)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "org": org, "bytes": len(body)})
}

// cleanup is the example job: it drops provider mirror rows of failed
// transactions older than the payload's number of hours (default 24).
func (p *Pack) cleanup(ctx context.Context, job sdk.Job) error {
	hours := 24
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &hours); err != nil {
			return fmt.Errorf("bad payload: %w", err)
		}
	}
	return p.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM pack_example_txn WHERE state = 'failed' AND created_at < now() - make_interval(hours => $1)`, hours)
		return err
	})
}

// onPayment handles payment.recorded: idempotent on the payment id.
func (p *Pack) onPayment(ctx context.Context, e sdk.Event) error {
	return p.mark(ctx, "payment.recorded", string(e.IDs["payment"]), string(e.IDs["site"]))
}
