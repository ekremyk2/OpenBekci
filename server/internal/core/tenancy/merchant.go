package tenancy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// Account subtypes of the money the merchant of record holds on the way to a site's bank account (the values of
// ledger.SubClearing and ledger.SubClientMoney; tenancy cannot import ledger).
const (
	subtypeClearing    = "clearing"
	subtypeClientMoney = "client_money"
)

// MerchantFieldError is the field of the merchant_change_blocked problem.
const merchantField = "settings." + SettingMerchantOfRecord

// OpenBalance is a clearing or client money balance of a site that is not zero.
type OpenBalance struct {
	SiteID  uuid.UUID `json:"site_id"`
	Account string    `json:"account"`
	Kind    string    `json:"kind"`
	Balance int64     `json:"balance"`
}

// openMerchantBalances lists the non-zero clearing and client money balances of the sites. While one exists the
// money of a site is in the hands of whoever was merchant of record when it was collected: switching the merchant
// would leave the next payout (posted by the new rule) against a clearing account the old rule filled.
func openMerchantBalances(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, sites []uuid.UUID) ([]OpenBalance, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT a.site_id, a.code, a.subtype, COALESCE(SUM(l.debit), 0)::bigint - COALESCE(SUM(l.credit), 0)::bigint
		FROM account a LEFT JOIN journal_line l ON l.org_id = a.org_id AND l.site_id = a.site_id AND l.account_id = a.id
		WHERE a.org_id = $1 AND a.site_id = ANY($2) AND a.subtype IN ($3, $4)
		GROUP BY a.site_id, a.code, a.subtype
		HAVING COALESCE(SUM(l.debit), 0) <> COALESCE(SUM(l.credit), 0)
		ORDER BY a.site_id, a.code`, orgID, sites, subtypeClearing, subtypeClientMoney)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenBalance
	for rows.Next() {
		var b OpenBalance
		if err := rows.Scan(&b.SiteID, &b.Account, &b.Kind, &b.Balance); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// merchantChangeBlocked is the 409 of a merchant of record change while money is on its way: the balances are in
// the params of the field error.
func merchantChangeBlocked(bal []OpenBalance) error {
	list := make([]map[string]any, 0, len(bal))
	for _, b := range bal {
		list = append(list, map[string]any{"site_id": b.SiteID.String(), "account": b.Account, "kind": b.Kind, "balance": b.Balance})
	}
	return &problem.Error{
		Code: problem.CodeMerchantChangeBlocked, Status: http.StatusConflict,
		Detail: "the merchant of record cannot change while a payment provider clearing or client money balance is open; settle or pay it out first",
		Fields: []problem.Field{{Field: merchantField, Code: "open_balance", Params: map[string]any{"balances": list}}},
	}
}

// merchantOverride is the site's own merchant of record setting, "" when it follows the organization.
func merchantOverride(settings map[string]any) string {
	if v, ok := settings[SettingMerchantOfRecord].(string); ok && ValidMerchantOfRecord(v) {
		return v
	}
	return ""
}

// guardOrgMerchantChange refuses a change of the organization's merchant of record while a site that follows it
// has a clearing or client money balance.
func guardOrgMerchantChange(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, before, next map[string]any) error {
	if EffectiveMerchantOfRecord(before, nil) == EffectiveMerchantOfRecord(next, nil) {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id, settings FROM site WHERE org_id = $1`, orgID)
	if err != nil {
		return err
	}
	var follow []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		if merchantOverride(m) == "" {
			follow = append(follow, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	bal, err := openMerchantBalances(ctx, tx, orgID, follow)
	if err != nil {
		return err
	}
	if len(bal) > 0 {
		return merchantChangeBlocked(bal)
	}
	return nil
}

// guardSiteMerchantChange does the same for the override of one site.
func guardSiteMerchantChange(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, before, next map[string]any) error {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT settings FROM organization WHERE id = $1`, orgID).Scan(&raw); err != nil {
		return err
	}
	org := map[string]any{}
	_ = json.Unmarshal(raw, &org)
	if EffectiveMerchantOfRecord(org, before) == EffectiveMerchantOfRecord(org, next) {
		return nil
	}
	bal, err := openMerchantBalances(ctx, tx, orgID, []uuid.UUID{siteID})
	if err != nil {
		return err
	}
	if len(bal) > 0 {
		return merchantChangeBlocked(bal)
	}
	return nil
}
