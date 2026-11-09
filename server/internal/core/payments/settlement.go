package payments

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SettlementInput is a provider payout.
type SettlementInput struct {
	Provider string
	// SiteID is the site the payout is reported for: the site whose bank account receives it when the site is
	// the merchant of record, and the site that takes the part of the net no transaction explains when the
	// organization is.
	SiteID uuid.UUID
	Ref    string
	// BankAccountCode is the bank account the net arrived in; empty means the site's default bank account.
	// It does not apply when the organization is the merchant of record (the money is in its bank account).
	BankAccountCode string
	SettledOn       string
	Net             money.Money
	// Gross and Fee are the payout's gross and fee when the provider reports them (informational).
	Gross, Fee *money.Money
	// TxnIDs are the provider transactions the payout covers; with the organization as merchant of record the
	// net is split by the sites of these payments.
	TxnIDs []string
}

// SettlementPosting is the part of a payout posted to one site.
type SettlementPosting struct {
	SiteID  uuid.UUID `json:"site_id"`
	EntryID uuid.UUID `json:"entry_id"`
	Net     int64     `json:"net"`
}

// SettlementResult is the outcome of RecordProviderSettlement.
type SettlementResult struct {
	Postings         []SettlementPosting
	MerchantOfRecord string
	// Duplicate is true when the payout was recorded before; the postings are those of the first time.
	Duplicate bool
}

// merchantOf resolves the effective merchant of record of a site inside tx.
func merchantOf(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site) (string, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT settings FROM organization WHERE id = $1`, orgID).Scan(&raw); err != nil {
		return "", err
	}
	org := map[string]any{}
	_ = json.Unmarshal(raw, &org)
	return tenancy.EffectiveMerchantOfRecord(org, site.Settings), nil
}

// MerchantOfRecord returns the effective merchant of record of a site (tenancy.MerchantSite or MerchantOrganization).
func (s *Service) MerchantOfRecord(ctx context.Context, orgID, siteID uuid.UUID) (string, error) {
	var out string
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, true, true)
		if err != nil {
			return err
		}
		out, err = merchantOf(ctx, tx, orgID, site)
		return err
	})
	return out, err
}

// RecordSettlement posts a payout and returns the entry of the first posting; see RecordProviderSettlement.
func (s *Service) RecordSettlement(ctx context.Context, orgID uuid.UUID, in SettlementInput) (entry uuid.UUID, duplicate bool, err error) {
	res, err := s.RecordProviderSettlement(ctx, orgID, in)
	if err != nil || len(res.Postings) == 0 {
		return uuid.Nil, res.Duplicate, err
	}
	return res.Postings[0].EntryID, res.Duplicate, nil
}

// RecordProviderSettlement posts a provider payout. A payout recorded before returns its postings with
// Duplicate set (idempotent per organization, provider and ref). What is posted depends on the merchant of
// record of the site (decision M5):
//
//   - site: the net moves from the provider's clearing account to the site's bank account (Dr bank, Cr clearing).
//   - organization: the net arrived in the organization's bank account, so each site's share (by the sites of
//     the payments the payout covers) moves from the site's clearing account to its client money account
//     (Dr 1195 client money, Cr clearing): the organization owes that money to the site until it passes it on.
func (s *Service) RecordProviderSettlement(ctx context.Context, orgID uuid.UUID, in SettlementInput) (SettlementResult, error) {
	var out SettlementResult
	if in.Provider == "" || in.Ref == "" {
		return out, bad("ref", "required")
	}
	err := s.run(ctx, orgID, "settlement", func(tx pgx.Tx) error {
		out = SettlementResult{}
		if err := providerLock(ctx, tx, orgID, "settlement", in.Provider, in.Ref); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT site_id, entry_id, net, merchant_of_record FROM provider_settlement WHERE org_id = $1 AND provider = $2 AND ref = $3 ORDER BY site_id`, orgID, in.Provider, in.Ref)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p SettlementPosting
			if err := rows.Scan(&p.SiteID, &p.EntryID, &p.Net, &out.MerchantOfRecord); err != nil {
				rows.Close()
				return err
			}
			out.Postings = append(out.Postings, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(out.Postings) > 0 {
			out.Duplicate = true
			return nil
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyPost, in.SiteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, in.SiteID, true, false)
		if err != nil {
			return err
		}
		if in.Net.Amount <= 0 || in.Net.Currency != ledger.Currency(site) {
			return bad("net", "invalid_value")
		}
		if _, err := ledger.ParseDate(in.SettledOn); err != nil {
			return bad("settled_on", "invalid_format")
		}
		mor, err := merchantOf(ctx, tx, orgID, site)
		if err != nil {
			return err
		}
		out.MerchantOfRecord = mor
		shares := []SettlementPosting{{SiteID: in.SiteID, Net: in.Net.Amount}}
		if mor == tenancy.MerchantOrganization {
			if in.BankAccountCode != "" {
				return bad("bank_account", "not_applicable")
			}
			if shares, err = splitByPayments(ctx, tx, orgID, in); err != nil {
				return err
			}
		}
		for i := range shares {
			sh := &shares[i]
			if sh.SiteID != in.SiteID {
				if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyPost, sh.SiteID); err != nil {
					return err
				}
			}
			ch, err := ledger.EnsureChart(ctx, tx, orgID, sh.SiteID)
			if err != nil {
				return err
			}
			clearing, err := ledger.EnsureAccount(ctx, tx, orgID, sh.SiteID, ledger.ClearingCode(in.Provider), "Clearing: "+in.Provider, "asset", ledger.SubClearing, nil)
			if err != nil {
				return err
			}
			var target uuid.UUID
			memo := "Settlement " + in.Provider + " " + in.Ref
			if mor == tenancy.MerchantOrganization {
				target, err = ledger.EnsureAccount(ctx, tx, orgID, sh.SiteID, ledger.ClientMoneyCode, "Client money held by the organization", "asset", ledger.SubClientMoney, nil)
				if err != nil {
					return err
				}
				memo += " (held by the organization)"
			} else {
				target = ch.Account(ledger.SubBank)
				if in.BankAccountCode != "" {
					if target = ch.AccountCode(in.BankAccountCode); target == uuid.Nil {
						return bad("bank_account", "invalid_value")
					}
					// a payout arrives in a bank account, not in an expense or a liability
					var typ, sub string
					if err := tx.QueryRow(ctx, `SELECT type, COALESCE(subtype, '') FROM account WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, sh.SiteID, target).Scan(&typ, &sub); err != nil {
						return err
					}
					if typ != "asset" || sub != ledger.SubBank {
						return bad("bank_account", "invalid_value")
					}
				}
			}
			sh.EntryID, err = ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: sh.SiteID, EntryDate: in.SettledOn, Memo: memo,
				SourceType: "provider_settlement", Lines: []ledger.Line{
					{AccountID: target, FundID: ch.OperatingFund, Debit: sh.Net},
					{AccountID: clearing, FundID: ch.OperatingFund, Credit: sh.Net},
				}})
			if err != nil {
				return err
			}
			var gross, fee *int64
			if len(shares) == 1 && in.Gross != nil && in.Fee != nil {
				gross, fee = &in.Gross.Amount, &in.Fee.Amount
			}
			if _, err := tx.Exec(ctx, `INSERT INTO provider_settlement (org_id, site_id, provider, ref, net, settled_on, entry_id, merchant_of_record, gross, fee) VALUES ($1,$2,$3,$4,$5,$6::date,$7,$8,$9,$10)`,
				orgID, sh.SiteID, in.Provider, in.Ref, sh.Net, in.SettledOn, sh.EntryID, mor, gross, fee); err != nil {
				return err
			}
			if err := ledger.Audit(ctx, tx, orgID, sh.SiteID, "payment.settle", "provider_settlement", sh.EntryID, nil, map[string]any{
				"provider": in.Provider, "ref": in.Ref, "net": sh.Net, "settled_on": in.SettledOn, "merchant_of_record": mor}); err != nil {
				return err
			}
		}
		out.Postings = shares
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, problem.NotFound("no such payment")
	}
	return out, err
}

// splitByPayments divides the net of a payout among the sites of the payments it covers. A payout that covers
// no known payment, or only part of its net, leaves the rest with the site it was reported for.
func splitByPayments(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, in SettlementInput) ([]SettlementPosting, error) {
	net := in.Net.Amount
	var matched map[uuid.UUID]int64
	var sum int64
	if len(in.TxnIDs) > 0 {
		matched = map[uuid.UUID]int64{}
		rows, err := tx.Query(ctx, `SELECT site_id, SUM(net) FROM payment WHERE org_id = $1 AND provider = $2 AND provider_txn_id = ANY($3) GROUP BY site_id`, orgID, in.Provider, in.TxnIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var site uuid.UUID
			var n int64
			if err := rows.Scan(&site, &n); err != nil {
				rows.Close()
				return nil, err
			}
			matched[site] = n
			sum += n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if sum == 0 {
		return []SettlementPosting{{SiteID: in.SiteID, Net: net}}, nil
	}
	ids := make([]uuid.UUID, 0, len(matched))
	for site := range matched {
		ids = append(ids, site)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	shares := make(map[uuid.UUID]int64, len(ids))
	if sum <= net {
		for _, site := range ids {
			shares[site] = matched[site]
		}
		shares[in.SiteID] += net - sum
	} else {
		// the provider paid less than the payments it covers (a refund or an adjustment): scale down
		var given int64
		for _, site := range ids {
			shares[site] = matched[site] * net / sum
			given += shares[site]
		}
		shares[ids[0]] += net - given
	}
	out := make([]SettlementPosting, 0, len(shares))
	for site, n := range shares {
		if n > 0 {
			out = append(out, SettlementPosting{SiteID: site, Net: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SiteID.String() < out[j].SiteID.String() })
	return out, nil
}

// SettlementRow is a recorded provider payout (one site's part of it).
type SettlementRow struct {
	Provider         string       `json:"provider"`
	Ref              string       `json:"ref"`
	SiteID           uuid.UUID    `json:"site_id"`
	Net              money.Money  `json:"net"`
	Gross            *money.Money `json:"gross"`
	Fee              *money.Money `json:"fee"`
	SettledOn        string       `json:"settled_on"`
	EntryID          uuid.UUID    `json:"entry_id"`
	MerchantOfRecord string       `json:"merchant_of_record"`
	CreatedAt        string       `json:"created_at"`
}

// ListSettlements lists the payouts recorded for a site, newest first (money.read). provider "" lists all.
func (s *Service) ListSettlements(ctx context.Context, orgID, siteID uuid.UUID, provider string, limit int, after *SettlementRow) ([]SettlementRow, error) {
	var out []SettlementRow
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := money.Code(ledger.Currency(site))
		curOn, curProv, curRef := "", "", ""
		if after != nil {
			curOn, curProv, curRef = after.SettledOn, after.Provider, after.Ref
		}
		rows, err := tx.Query(ctx, `SELECT provider, ref, site_id, net, gross, fee, to_char(settled_on, 'YYYY-MM-DD'), entry_id, merchant_of_record, to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
			FROM provider_settlement WHERE org_id = $1 AND site_id = $2 AND ($3 = '' OR provider = $3)
			AND ($4 = '' OR (settled_on, provider, ref) < ($4::date, $5, $6))
			ORDER BY settled_on DESC, provider DESC, ref DESC LIMIT $7`, orgID, siteID, provider, curOn, curProv, curRef, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r SettlementRow
			var net int64
			var gross, fee *int64
			if err := rows.Scan(&r.Provider, &r.Ref, &r.SiteID, &net, &gross, &fee, &r.SettledOn, &r.EntryID, &r.MerchantOfRecord, &r.CreatedAt); err != nil {
				return err
			}
			r.Net = money.New(net, cur)
			if gross != nil {
				g := money.New(*gross, cur)
				r.Gross = &g
			}
			if fee != nil {
				f := money.New(*fee, cur)
				r.Fee = &f
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// ProviderClearing is the clearing balance of one provider at a site.
type ProviderClearing struct {
	Provider string      `json:"provider"`
	Balance  money.Money `json:"balance"`
}

// ClearingBalances is what a site has not yet received from its providers, and the client money the
// organization holds for it.
type ClearingBalances struct {
	SiteID           uuid.UUID          `json:"site_id"`
	MerchantOfRecord string             `json:"merchant_of_record"`
	Providers        []ProviderClearing `json:"providers"`
	// ClientMoneyOwed is what the organization holds for the site and has not passed on (merchant of record =
	// organization); zero otherwise.
	ClientMoneyOwed money.Money `json:"client_money_owed"`
}

// ClearingBalancesOf returns the clearing balance per provider and the client money balance of a site (money.read).
func (s *Service) ClearingBalancesOf(ctx context.Context, orgID, siteID uuid.UUID) (ClearingBalances, error) {
	var out ClearingBalances
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := money.Code(ledger.Currency(site))
		out = ClearingBalances{SiteID: siteID, Providers: []ProviderClearing{}, ClientMoneyOwed: money.Zero(cur)}
		if out.MerchantOfRecord, err = merchantOf(ctx, tx, orgID, site); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, code, COALESCE(subtype, '') FROM account WHERE org_id = $1 AND site_id = $2 AND subtype IN ($3, $4) ORDER BY code`, orgID, siteID, ledger.SubClearing, ledger.SubClientMoney)
		if err != nil {
			return err
		}
		type acc struct {
			id        uuid.UUID
			code, sub string
		}
		var accs []acc
		for rows.Next() {
			var a acc
			if err := rows.Scan(&a.id, &a.code, &a.sub); err != nil {
				rows.Close()
				return err
			}
			accs = append(accs, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, a := range accs {
			d, c, err := ledger.Balance(ctx, tx, orgID, siteID, a.id, ledger.BalanceFilter{})
			if err != nil {
				return err
			}
			if a.sub == ledger.SubClientMoney {
				out.ClientMoneyOwed = money.New(d-c, cur)
				continue
			}
			out.Providers = append(out.Providers, ProviderClearing{Provider: a.code[len("1190."):], Balance: money.New(d-c, cur)})
		}
		return nil
	})
	return out, err
}
