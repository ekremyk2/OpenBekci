package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// moneyNotifier turns the money modules' events (ledger.Notification) into the
// shared notification job (jobs.NotifyArgs, kind notifications.deliver), in the
// business transaction: it names the recipients (the debtor person, or the
// people currently living on the unit side) and fills what the templates show,
// so the dispatcher needs no knowledge of money.
type moneyNotifier struct{ out jobs.Notifier }

var _ ledger.Notifier = moneyNotifier{}

// moneyTypes maps the money event types onto the dispatcher's vocabulary.
var moneyTypes = map[string]string{
	ledger.NotifyPaymentConfirmed: "payment.confirmed",
	ledger.NotifyPaymentRejected:  "payment.rejected",
	ledger.NotifyDunningReminder:  "dunning.reminder",
	ledger.NotifyStatementSend:    "statement.sent",
	ledger.NotifyReceiptSend:      "payment.receipt",
}

// Enqueue implements ledger.Notifier.
func (n moneyNotifier) Enqueue(ctx context.Context, tx pgx.Tx, e ledger.Notification) error {
	typ, ok := moneyTypes[e.Type]
	if !ok {
		typ = e.Type
	}
	p := map[string]any{}
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("money notification %s: payload: %w", e.Type, err)
		}
	}
	unit, kind, person := uuidOf(p["unit_id"]), strOf(p["debtor_kind"]), uuidOf(p["person_id"])
	if pay := uuidOf(p["payment_id"]); pay != uuid.Nil && unit == uuid.Nil {
		// events that only name the payment (a rejected declaration): the debtor is on the payment
		var k *string
		var u, pr *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT unit_id, debtor_kind, debtor_person_id FROM payment WHERE org_id = $1 AND id = $2`, e.OrgID, pay).Scan(&u, &k, &pr)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if u != nil {
			unit = *u
		}
		if k != nil {
			kind = *k
		}
		if pr != nil && person == uuid.Nil {
			person = *pr
		}
	}
	if deb, ok := p["debtor"].(map[string]any); ok { // statements name the debtor as an object
		if unit == uuid.Nil {
			unit = uuidOf(deb["unit_id"])
		}
		if kind == "" {
			kind = strOf(deb["debtor_kind"])
		}
		if person == uuid.Nil {
			person = uuidOf(deb["person_id"])
		}
	}

	var recipients []jobs.Recipient
	switch {
	case person != uuid.Nil:
		recipients = append(recipients, jobs.Recipient{PersonID: &person})
	case unit != uuid.Nil:
		kinds := []string{"owner", "tenant"}
		switch kind {
		case "owner":
			kinds = []string{"owner"}
		case "tenant":
			kinds = []string{"tenant"}
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT person_id FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND kind = ANY($3)
			AND valid_from <= CURRENT_DATE AND (valid_to IS NULL OR valid_to > CURRENT_DATE)`, e.OrgID, unit, kinds)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			recipients = append(recipients, jobs.Recipient{PersonID: &id})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	if len(recipients) == 0 {
		return nil // nobody to tell: a unit without residents has no one to notify
	}

	// what the templates show
	var locale string
	if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, e.OrgID).Scan(&locale); err != nil {
		return err
	}
	if unit != uuid.Nil {
		var door string
		if err := tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id = $1 AND id = $2`, e.OrgID, unit).Scan(&door); err == nil {
			p["unit"] = door
		}
	}
	for _, k := range []string{"amount", "amount_due"} {
		if n, ok := p[k].(float64); ok && n == math.Trunc(n) { // a bare whole number of minor units: it is in the site's currency
			var cur string
			if err := tx.QueryRow(ctx, `SELECT currency FROM site WHERE org_id = $1 AND id = $2`, e.OrgID, e.SiteID).Scan(&cur); err == nil {
				p[k] = map[string]any{"amount": int64(n), "currency": cur}
			}
		}
		if s := formatMoney(p[k], locale); s != "" {
			p[k] = s
		}
	}
	if _, ok := p["amount"]; !ok {
		if s, ok := p["amount_due"]; ok {
			p["amount"] = s
		}
	}
	args := jobs.NotifyArgs{OrgID: e.OrgID, SiteID: &e.SiteID, Type: typ, Recipients: recipients, Payload: p}
	if ch := strOf(p["channel"]); ch == jobs.ChannelSMS || ch == jobs.ChannelEmail || ch == jobs.ChannelInApp {
		args.Channels = []string{jobs.ChannelInApp, ch}
		if ch == jobs.ChannelInApp {
			args.Channels = []string{jobs.ChannelInApp}
		}
	}
	return n.out.Notify(ctx, tx, args)
}

func uuidOf(v any) uuid.UUID {
	s, _ := v.(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}

// formatMoney renders a {"amount":minor,"currency":"TRY"} object in the
// organization's language; anything else yields "".
func formatMoney(v any, locale string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	var mm money.Money
	if err := json.Unmarshal(b, &mm); err != nil {
		return ""
	}
	s, err := mm.Format(locale)
	if err != nil {
		return mm.String()
	}
	return s
}
