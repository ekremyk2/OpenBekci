// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() (fs.FS, error) { return fs.Sub(migrationFiles, "migrations") }

// account says whose iyzico account a checkout was created on: the merchant of
// record and the site of its target. The core hands Status and Refund nothing
// but the transaction id, so the pack remembers the account per checkout
// (table pack_tr_iyzico_checkout) and resolves the same account later.
type account struct {
	Site     sdk.ID
	Merchant string
	// Basket is the basketId the checkout was created with: the payment target
	// of a checkout is read from here, never from what iyzico echoes back.
	Basket string
	// PaymentID is the iyzico paymentId once the payment was seen paid, else empty.
	PaymentID string
}

func (a account) scope() sdk.Scope { return sdk.AccountScope(a.Merchant, a.Site) }

// accountStore keeps the account of each checkout token.
type accountStore interface {
	put(ctx context.Context, token string, a account) error
	get(ctx context.Context, token string) (account, bool, error)
	// setPayment remembers the iyzico paymentId of a paid checkout (the daily report names payments by it).
	setPayment(ctx context.Context, token, paymentID string) error
	// tokensOf returns the known checkout tokens of paymentIds.
	tokensOf(ctx context.Context, paymentIDs []string) ([]string, error)
}

type sqlAccounts struct{ svc sdk.Services }

func (s sqlAccounts) put(ctx context.Context, token string, a account) error {
	org, ok := sdk.OrgFrom(ctx)
	if !ok {
		return sdk.ErrNoOrg
	}
	return s.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO pack_tr_iyzico_checkout (org_id, token, site_id, merchant, basket_id) VALUES ($1::uuid, $2, $3::uuid, $4, $5)
			ON CONFLICT (org_id, token) DO NOTHING`, string(org), token, string(a.Site), a.Merchant, a.Basket)
		return err
	})
}

func (s sqlAccounts) get(ctx context.Context, token string) (account, bool, error) {
	var a account
	var site string
	err := s.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		return tx.QueryRow(ctx, `SELECT site_id::text, merchant, basket_id, COALESCE(payment_id, '') FROM pack_tr_iyzico_checkout WHERE token = $1`, token).Scan(&site, &a.Merchant, &a.Basket, &a.PaymentID)
	})
	if err == sdk.ErrNoRows {
		return account{}, false, nil
	}
	a.Site = sdk.ID(site)
	return a, err == nil, err
}

func (s sqlAccounts) setPayment(ctx context.Context, token, paymentID string) error {
	return s.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE pack_tr_iyzico_checkout SET payment_id = $2 WHERE token = $1 AND payment_id IS DISTINCT FROM $2`, token, paymentID)
		return err
	})
}

func (s sqlAccounts) tokensOf(ctx context.Context, paymentIDs []string) ([]string, error) {
	var out []string
	err := s.svc.DB().InTx(ctx, func(tx sdk.Tx) error {
		rows, err := tx.Query(ctx, `SELECT token FROM pack_tr_iyzico_checkout WHERE payment_id = ANY($1) ORDER BY token`, paymentIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func (pr *iyzico) accounts() accountStore {
	if pr.p.accounts != nil {
		return pr.p.accounts
	}
	return sqlAccounts{pr.p.svc}
}

// accountOf is the account a token was checked out on; a payment the pack
// never created a checkout for (no row) is looked up in the organization's.
func (pr *iyzico) accountOf(ctx context.Context, token string) (account, error) {
	a, ok, err := pr.accounts().get(ctx, token)
	if err != nil {
		return account{}, err
	}
	if !ok {
		return account{Merchant: sdk.MerchantOrganization}, nil
	}
	return a, nil
}

// creds reads the merchant account at a settings scope (sdk.AccountScope of the
// merchant of record): no fallback from one scope to the other, because whose
// account collects the money is the organization's decision (M5).
func (pr *iyzico) creds(ctx context.Context, sc sdk.Scope) (iyzicoCreds, error) {
	doc, err := pr.p.svc.Settings().All(ctx, sc)
	if err != nil {
		return iyzicoCreds{}, err
	}
	key, _ := doc["tr_payments_iyzico_api_key"].(string)
	secret, _ := doc["tr_payments_iyzico_secret_key"].(string)
	if key == "" || secret == "" {
		return iyzicoCreds{}, fmt.Errorf("no iyzico api key and secret key are configured for this account: %w", sdk.ErrCredentials)
	}
	if en, _ := doc["tr_payments_iyzico_enabled"].(bool); !en {
		return iyzicoCreds{}, fmt.Errorf("iyzico is not enabled in the settings of this account: %w", sdk.ErrUnsupported)
	}
	sandbox, _ := doc["tr_payments_iyzico_sandbox"].(bool)
	base, _ := doc["tr_payments_iyzico_callback_base_url"].(string)
	if base == "" && sc.Kind == sdk.ScopeSite {
		// the public address of the server is not a property of the account
		if org, err := pr.p.svc.Settings().All(ctx, sdk.OrgScope()); err == nil {
			base, _ = org["tr_payments_iyzico_callback_base_url"].(string)
		}
	}
	return iyzicoCreds{APIKey: key, SecretKey: secret, Sandbox: sandbox, CallbackBase: strings.TrimRight(base, "/")}, nil
}

func (pr *iyzico) client(ctx context.Context, sc sdk.Scope) (*iyzicoClient, error) {
	c, err := pr.creds(ctx, sc)
	if err != nil {
		return nil, err
	}
	return &iyzicoClient{http: pr.p.client(), creds: c}, nil
}
