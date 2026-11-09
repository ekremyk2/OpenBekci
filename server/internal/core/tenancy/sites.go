package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/extval"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Site settings keys (typed accessors below).
const (
	SettingTransparencyToOwners  = "transparency_to_owners"
	SettingTenantsArePortalUsers = "tenants_are_portal_users"
	SettingDirectoryEnabled      = "directory_enabled"
	SettingAppurtenanceDefault   = "appurtenance_default"
	// SettingVisitorRetentionDays is how long the visitor logs are kept (7 to 365).
	SettingVisitorRetentionDays = "visitor_retention_days"
	SettingPayerDefaults        = "payer_defaults"
	// SettingMerchantOfRecord is whose provider account collects card payments (decision M5): "site" (the
	// default: the site collects into its own provider account) or "organization" (the management company
	// collects centrally and holds the net as client money owed to each site). Set on the organization, with
	// an optional override per site; under the key payments.merchant_of_record in the settings document.
	SettingMerchantOfRecord = "payments.merchant_of_record"
	// SettingPaymentProvider optionally names the payment provider ("<packid>" or "<packid>.<name>") that card
	// checkout uses when several are enabled; organization setting with an optional per-site override.
	SettingPaymentProvider = "payments.provider"
)

// Merchant of record values.
const (
	MerchantSite         = "site"
	MerchantOrganization = "organization"
)

// ValidMerchantOfRecord reports whether v is a merchant of record value.
func ValidMerchantOfRecord(v any) bool {
	s, ok := v.(string)
	return ok && (s == MerchantSite || s == MerchantOrganization)
}

// EffectiveMerchantOfRecord resolves the merchant of record of a site: the site's own setting, else the
// organization's, else MerchantSite.
func EffectiveMerchantOfRecord(orgSettings, siteSettings map[string]any) string {
	for _, m := range []map[string]any{siteSettings, orgSettings} {
		if v, ok := m[SettingMerchantOfRecord].(string); ok && ValidMerchantOfRecord(v) {
			return v
		}
	}
	return MerchantSite
}

// EffectivePaymentProvider is the provider the site or organization named, "" when none.
func EffectivePaymentProvider(orgSettings, siteSettings map[string]any) string {
	for _, m := range []map[string]any{siteSettings, orgSettings} {
		if v, ok := m[SettingPaymentProvider].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// validatePaymentSettings checks the payments.* keys of an organization or site settings document.
func validatePaymentSettings(settings map[string]any) []problem.Field {
	var f []problem.Field
	if v, ok := settings[SettingMerchantOfRecord]; ok && v != nil && !ValidMerchantOfRecord(v) {
		f = append(f, problem.Field{Field: "settings." + SettingMerchantOfRecord, Code: "invalid_value", Params: map[string]any{"allowed": []string{MerchantSite, MerchantOrganization}}})
	}
	if v, ok := settings[SettingPaymentProvider]; ok && v != nil {
		if s, isStr := v.(string); !isStr || len(s) > 64 {
			f = append(f, problem.Field{Field: "settings." + SettingPaymentProvider, Code: "invalid_value"})
		}
	}
	return f
}

// Appurtenance modes (S2).
const (
	Appurtenance = "appurtenance"
	Independent  = "independent"
)

// Site is a site as the API shows it. The JSON tags are the audit field names.
type Site struct {
	ID                   uuid.UUID      `json:"id"`
	Name                 string         `json:"name"`
	Address              map[string]any `json:"address"`
	Timezone             string         `json:"timezone"`
	Currency             string         `json:"currency"`
	FiscalYearStartMonth int            `json:"fiscal_year_start_month"`
	LockDate             *string        `json:"lock_date"`
	Settings             map[string]any `json:"settings"`
	BankDetails          map[string]any `json:"bank_details"`
	Status               string         `json:"status"`
	Version              int            `json:"version"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

// SiteSettings are the typed settings of a site; unknown keys in the stored
// document are preserved by the service but not interpreted.
type SiteSettings struct {
	TransparencyToOwners  bool
	TenantsArePortalUsers bool
	DirectoryEnabled      bool
	// AppurtenanceDefault is Appurtenance or Independent: whether a parking
	// spot or storage room without an override is listed under a flat (S2).
	AppurtenanceDefault string
}

// TypedSettings reads the typed settings with their defaults (tenants are portal
// users, parking and storage are appurtenances).
func (s Site) TypedSettings() SiteSettings {
	out := SiteSettings{TenantsArePortalUsers: true, AppurtenanceDefault: Appurtenance}
	if v, ok := s.Settings[SettingTransparencyToOwners].(bool); ok {
		out.TransparencyToOwners = v
	}
	if v, ok := s.Settings[SettingTenantsArePortalUsers].(bool); ok {
		out.TenantsArePortalUsers = v
	}
	if v, ok := s.Settings[SettingDirectoryEnabled].(bool); ok {
		out.DirectoryEnabled = v
	}
	if v, ok := s.Settings[SettingAppurtenanceDefault].(string); ok && (v == Appurtenance || v == Independent) {
		out.AppurtenanceDefault = v
	}
	return out
}

type siteAudit struct {
	Name                 string         `json:"name"`
	Address              map[string]any `json:"address"`
	Timezone             string         `json:"timezone"`
	Currency             string         `json:"currency"`
	FiscalYearStartMonth int            `json:"fiscal_year_start_month"`
	LockDate             *string        `json:"lock_date"`
	Settings             map[string]any `json:"settings"`
	BankDetails          map[string]any `json:"bank_details"`
	Status               string         `json:"status"`
}

func (s Site) audit() siteAudit {
	return siteAudit{s.Name, s.Address, s.Timezone, s.Currency, s.FiscalYearStartMonth, s.LockDate, s.Settings, s.BankDetails, s.Status}
}

const siteColumns = `id, name, address, timezone, currency, fiscal_year_start_month, to_char(lock_date, 'YYYY-MM-DD'), settings, bank_details, status, version, created_at, updated_at`

func scanSite(row pgx.Row) (Site, error) {
	var s Site
	var addr, settings, bank []byte
	if err := row.Scan(&s.ID, &s.Name, &addr, &s.Timezone, &s.Currency, &s.FiscalYearStartMonth, &s.LockDate, &settings, &bank, &s.Status, &s.Version, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return Site{}, err
	}
	for raw, dst := range map[*[]byte]*map[string]any{&addr: &s.Address, &settings: &s.Settings, &bank: &s.BankDetails} {
		m := map[string]any{}
		if len(*raw) > 0 {
			_ = json.Unmarshal(*raw, &m)
		}
		*dst = m
	}
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return s, nil
}

// LoadSite reads a site inside tx. It answers 404 for an unknown site and,
// unless allowOffboarded, 403 site_offboarded for one that was handed over.
func LoadSite(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, forUpdate, allowOffboarded bool) (Site, error) {
	q := `SELECT ` + siteColumns + ` FROM site WHERE org_id = $1 AND id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	s, err := scanSite(tx.QueryRow(ctx, q, orgID, siteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, problem.NotFound("no such site")
	}
	if err != nil {
		return Site{}, problem.Internal(fmt.Errorf("load site: %w", err))
	}
	if s.Status == "offboarded" && !allowOffboarded {
		return Site{}, problem.New(403, problem.CodeSiteOffboarded, "this site was handed over and is no longer managed here")
	}
	return s, nil
}

var (
	currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)
	codeRE     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

func validateSettings(settings map[string]any) []problem.Field {
	var f []problem.Field
	for _, k := range []string{SettingTransparencyToOwners, SettingTenantsArePortalUsers, SettingDirectoryEnabled} {
		if v, ok := settings[k]; ok {
			if _, isBool := v.(bool); !isBool {
				f = append(f, problem.Field{Field: "settings." + k, Code: "invalid_type"})
			}
		}
	}
	if v, ok := settings[SettingVisitorRetentionDays]; ok {
		if n, isNum := v.(float64); !isNum || n != float64(int(n)) {
			f = append(f, problem.Field{Field: "settings." + SettingVisitorRetentionDays, Code: "invalid_type"})
		} else if n < 7 {
			f = append(f, problem.Field{Field: "settings." + SettingVisitorRetentionDays, Code: "too_small", Params: map[string]any{"min": 7}})
		} else if n > 365 {
			f = append(f, problem.Field{Field: "settings." + SettingVisitorRetentionDays, Code: "too_large", Params: map[string]any{"max": 365}})
		}
	}
	if v, ok := settings[SettingAppurtenanceDefault]; ok {
		if s, isStr := v.(string); !isStr || (s != Appurtenance && s != Independent) {
			f = append(f, problem.Field{Field: "settings." + SettingAppurtenanceDefault, Code: "invalid_value"})
		}
	}
	f = append(f, validatePaymentSettings(settings)...)
	return f
}

var siteFields = []string{"name", "address", "timezone", "currency", "fiscal_year_start_month", "lock_date", "settings", "bank_details"}

// applySite validates d over base and returns the result. create makes name required.
func applySite(base Site, d Doc, create bool) (Site, error) {
	allowed := siteFields
	if create {
		allowed = append(append([]string{}, siteFields...), "blocks")
	}
	fields := d.Unknown(allowed...)
	s := base
	if d.Has("name") {
		var v string
		if err := d.Get("name", &v); err != nil {
			return s, err
		}
		if n := len([]rune(v)); n < 1 || n > 200 {
			fields = append(fields, problem.Field{Field: "name", Code: "invalid_value"})
		}
		s.Name = v
	} else if create {
		fields = append(fields, problem.Field{Field: "name", Code: "required"})
	}
	for _, k := range []struct {
		name  string
		dst   *map[string]any
		merge bool
	}{{"address", &s.Address, false}, {"bank_details", &s.BankDetails, false}, {"settings", &s.Settings, true}} {
		if !d.Has(k.name) {
			continue
		}
		var m map[string]any
		if err := d.Get(k.name, &m); err != nil {
			return s, err
		}
		if m == nil {
			m = map[string]any{}
		}
		if k.merge {
			merged := map[string]any{}
			for kk, vv := range *k.dst {
				merged[kk] = vv
			}
			for kk, vv := range m {
				if vv == nil {
					delete(merged, kk)
				} else {
					merged[kk] = vv
				}
			}
			m = merged
		}
		*k.dst = m
	}
	fields = append(fields, validateSettings(s.Settings)...)
	if d.Has("timezone") {
		var v string
		if err := d.Get("timezone", &v); err != nil {
			return s, err
		}
		if _, err := time.LoadLocation(v); err != nil || v == "" || v == "Local" {
			fields = append(fields, problem.Field{Field: "timezone", Code: "invalid_value"})
		}
		s.Timezone = v
	}
	if d.Has("currency") {
		var v string
		if err := d.Get("currency", &v); err != nil {
			return s, err
		}
		if !currencyRE.MatchString(v) {
			fields = append(fields, problem.Field{Field: "currency", Code: "invalid_format"})
		}
		s.Currency = v
	}
	if d.Has("fiscal_year_start_month") {
		var v int
		if err := d.Get("fiscal_year_start_month", &v); err != nil {
			return s, err
		}
		if v < 1 || v > 12 {
			fields = append(fields, problem.Field{Field: "fiscal_year_start_month", Code: "invalid_value"})
		}
		s.FiscalYearStartMonth = v
	}
	if d.Has("lock_date") {
		if d.IsNull("lock_date") {
			s.LockDate = nil
		} else {
			var v string
			if err := d.Get("lock_date", &v); err != nil {
				return s, err
			}
			if _, err := time.Parse("2006-01-02", v); err != nil {
				fields = append(fields, problem.Field{Field: "lock_date", Code: "invalid_format"})
			}
			s.LockDate = &v
		}
	}
	if len(fields) > 0 {
		return s, problem.Validation(fields...)
	}
	return s, nil
}

// ListSites returns the sites the caller can read (all of them with an
// organization-wide grant, else the granted sites).
func (s *Service) ListSites(ctx context.Context, orgID uuid.UUID, p PageParams) ([]Site, string, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	orgWide, siteIDs, _ := authz.GrantsFromContext(ctx).ScopeIDs(PermSiteRead)
	var out []Site
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+siteColumns+` FROM site
			WHERE org_id = $1 AND id > $2 AND ($3 OR id = ANY($4)) ORDER BY id LIMIT $5`, orgID, after, orgWide, siteIDs, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			st, err := scanSite(rows)
			if err != nil {
				return err
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", DBProblem(err, "list sites")
	}
	items, next := Page(out, limit, func(x Site) uuid.UUID { return x.ID })
	return items, next, nil
}

// GetSite returns a site. It needs site.read for the site.
func (s *Service) GetSite(ctx context.Context, orgID, siteID uuid.UUID) (Site, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteRead); err != nil {
		return Site{}, err
	}
	var out Site
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		out, err = loadSiteFor(ctx, tx, PermSiteRead, orgID, siteID, false, true)
		return err
	})
	if err != nil {
		return Site{}, DBProblem(err, "get site")
	}
	return out, nil
}

// checkIBAN checks the iban of the bank details of a site, when it has one, with
// the validators of the packs enabled for the organization (the Turkish IBAN
// rules); a failure is a field error on bank_details.iban.
func (s *Service) checkIBAN(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, bank map[string]any) error {
	iban, _ := bank["iban"].(string)
	if iban == "" {
		return nil
	}
	if f := extval.Check(ctx, s.validators, tx, orgID, extval.KindIBAN, "bank_details.iban", iban); f != nil {
		return problem.Validation(*f)
	}
	return nil
}

// CreateSite creates a site with its hidden block (or the named blocks) and the
// Law 634 allocation keys (arsa_payi by share, equal per unit). It needs site.create.
func (s *Service) CreateSite(ctx context.Context, orgID uuid.UUID, d Doc) (Site, error) {
	if err := authz.Require(ctx, PermSiteCreate, authz.Scope{OrgID: orgID}); err != nil {
		return Site{}, err
	}
	var blocks []string
	if d.Has("blocks") {
		if err := d.Get("blocks", &blocks); err != nil {
			return Site{}, err
		}
	}
	var out Site
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var tz string
		if err := tx.QueryRow(ctx, `SELECT timezone FROM organization WHERE id = $1`, orgID).Scan(&tz); err != nil {
			return err
		}
		base := Site{Address: map[string]any{}, Settings: map[string]any{}, BankDetails: map[string]any{}, Timezone: tz, Currency: "TRY", FiscalYearStartMonth: 1}
		site, err := applySite(base, d, true)
		if err != nil {
			return err
		}
		if err := s.checkIBAN(ctx, tx, orgID, site.BankDetails); err != nil {
			return err
		}
		site.ID = id.New()
		addr, _ := json.Marshal(site.Address)
		settings, _ := json.Marshal(site.Settings)
		bank, _ := json.Marshal(site.BankDetails)
		out, err = scanSite(tx.QueryRow(ctx, `
			INSERT INTO site (id, org_id, name, address, timezone, currency, fiscal_year_start_month, lock_date, settings, bank_details)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10) RETURNING `+siteColumns,
			site.ID, orgID, site.Name, addr, site.Timezone, site.Currency, site.FiscalYearStartMonth, site.LockDate, settings, bank))
		if err != nil {
			return err
		}
		// the default late fee (5 % a month, no grace; a legal preset may change it) applies to every due date, also dues from before the site was set up
		if _, err := tx.Exec(ctx, `INSERT INTO late_fee_policy (id, org_id, site_id, from_on, rate_bps_per_month, grace_days, is_default)
			VALUES ($1, $2, $3, DATE '1970-01-01', 500, 0, true)`, id.New(), orgID, out.ID); err != nil {
			return err
		}
		if len(blocks) == 0 {
			// a single building: one hidden block (S1)
			if _, err := tx.Exec(ctx, `INSERT INTO block (id, org_id, site_id, name, hidden) VALUES ($1, $2, $3, 'Main', true)`, id.New(), orgID, out.ID); err != nil {
				return err
			}
		}
		for i, name := range blocks {
			if name == "" {
				return problem.Validation(problem.Field{Field: fmt.Sprintf("blocks.%d", i), Code: "invalid_value"})
			}
			if _, err := tx.Exec(ctx, `INSERT INTO block (id, org_id, site_id, name, sort_order) VALUES ($1, $2, $3, $4, $5)`, id.New(), orgID, out.ID, name, i); err != nil {
				return err
			}
		}
		for _, k := range DefaultKeys {
			if _, err := tx.Exec(ctx, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind, is_default) VALUES ($1, $2, $3, $4, $5, $6, true)`,
				id.New(), orgID, out.ID, k.Code, k.Name, k.Kind); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("site", nil, out.audit())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &out.ID, Action: "site.create", TargetType: "site", TargetID: out.ID, Diff: diff}); err != nil {
			return err
		}
		if s.siteHook != nil {
			if err := s.siteHook.SiteCreated(ctx, tx, orgID, out.ID); err != nil {
				return err
			}
			// the hook may have filled settings
			out, err = LoadSite(ctx, tx, orgID, out.ID, false, true)
			return err
		}
		return nil
	})
	if err != nil {
		return Site{}, DBProblem(err, "site")
	}
	return out, nil
}

// UpdateSite applies d if the site is still at expectedVersion. It needs
// site.manage for the site; a site hand-over (offboarding) closes it to changes.
func (s *Service) UpdateSite(ctx context.Context, orgID, siteID uuid.UUID, expectedVersion int, d Doc) (Site, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteManage); err != nil {
		return Site{}, err
	}
	var out Site
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := loadSiteFor(ctx, tx, PermSiteManage, orgID, siteID, true, false)
		if err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		next, err := applySite(before, d, false)
		if err != nil {
			return err
		}
		if d.Has("bank_details") {
			if err := s.checkIBAN(ctx, tx, orgID, next.BankDetails); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("site", before.audit(), next.audit())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		if merchantOverride(before.Settings) != merchantOverride(next.Settings) {
			// whose provider account collects the money is the organization's decision, not a site manager's,
			// and it cannot move while money is on its way
			if err := authz.Require(ctx, PermOrgManage, authz.Scope{OrgID: orgID}); err != nil {
				return err
			}
			if err := guardSiteMerchantChange(ctx, tx, orgID, siteID, before.Settings, next.Settings); err != nil {
				return err
			}
		}
		if _, changed := diff["lock_date"]; changed {
			// changing the lock date is sensitive (backend plan 16): needs a recent re-authentication
			if err := authz.PrincipalFromContext(ctx).RequireReauth(time.Now()); err != nil {
				return err
			}
		}
		addr, _ := json.Marshal(next.Address)
		settings, _ := json.Marshal(next.Settings)
		bank, _ := json.Marshal(next.BankDetails)
		out, err = scanSite(tx.QueryRow(ctx, `
			UPDATE site SET name = $3, address = $4, timezone = $5, currency = $6, fiscal_year_start_month = $7, lock_date = $8::date,
			       settings = $9, bank_details = $10, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2 RETURNING `+siteColumns,
			orgID, siteID, next.Name, addr, next.Timezone, next.Currency, next.FiscalYearStartMonth, next.LockDate, settings, bank))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "site.update", TargetType: "site", TargetID: siteID, Diff: diff})
	})
	if errors.Is(err, ErrVersion) {
		return Site{}, VersionConflict("site")
	}
	if err != nil {
		return Site{}, DBProblem(err, "update site")
	}
	return out, nil
}

// Block is a building of a site.
type Block struct {
	ID        uuid.UUID `json:"id"`
	SiteID    uuid.UUID `json:"site_id"`
	Name      string    `json:"name"`
	Hidden    bool      `json:"hidden"`
	SortOrder int       `json:"sort_order"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const blockColumns = `id, site_id, name, hidden, sort_order, version, created_at, updated_at`

func scanBlock(row pgx.Row) (Block, error) {
	var b Block
	err := row.Scan(&b.ID, &b.SiteID, &b.Name, &b.Hidden, &b.SortOrder, &b.Version, &b.CreatedAt, &b.UpdatedAt)
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	return b, err
}

type blockAudit struct {
	Name      string `json:"name"`
	Hidden    bool   `json:"hidden"`
	SortOrder int    `json:"sort_order"`
}

// ListBlocks lists a site's blocks. It needs site.read for the site.
func (s *Service) ListBlocks(ctx context.Context, orgID, siteID uuid.UUID) ([]Block, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteRead); err != nil {
		return nil, err
	}
	out := []Block{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := loadSiteFor(ctx, tx, PermSiteRead, orgID, siteID, false, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+blockColumns+` FROM block WHERE org_id = $1 AND site_id = $2 ORDER BY sort_order, name`, orgID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBlock(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, DBProblem(err, "list blocks")
	}
	return out, nil
}

// CreateBlock adds a block to a site. It needs site.manage for the site.
func (s *Service) CreateBlock(ctx context.Context, orgID, siteID uuid.UUID, d Doc) (Block, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteManage); err != nil {
		return Block{}, err
	}
	b := Block{ID: id.New(), SiteID: siteID}
	if err := applyBlock(&b, d, true); err != nil {
		return Block{}, err
	}
	var out Block
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := loadSiteFor(ctx, tx, PermSiteManage, orgID, siteID, false, false); err != nil {
			return err
		}
		var err error
		out, err = scanBlock(tx.QueryRow(ctx, `INSERT INTO block (id, org_id, site_id, name, hidden, sort_order) VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+blockColumns,
			b.ID, orgID, siteID, b.Name, b.Hidden, b.SortOrder))
		if err != nil {
			return err
		}
		diff, err := audit.Diff("block", nil, blockAudit{out.Name, out.Hidden, out.SortOrder})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "block.create", TargetType: "block", TargetID: out.ID, Diff: diff})
	})
	if err != nil {
		return Block{}, DBProblem(err, "block")
	}
	return out, nil
}

func applyBlock(b *Block, d Doc, create bool) error {
	fields := d.Unknown("name", "hidden", "sort_order")
	if d.Has("name") {
		var v string
		if err := d.Get("name", &v); err != nil {
			return err
		}
		if n := len([]rune(v)); n < 1 || n > 100 {
			fields = append(fields, problem.Field{Field: "name", Code: "invalid_value"})
		}
		b.Name = v
	} else if create {
		fields = append(fields, problem.Field{Field: "name", Code: "required"})
	}
	if err := d.Get("hidden", &b.Hidden); err != nil {
		return err
	}
	if err := d.Get("sort_order", &b.SortOrder); err != nil {
		return err
	}
	if len(fields) > 0 {
		return problem.Validation(fields...)
	}
	return nil
}

// UpdateBlock renames, hides or reorders a block. It needs site.manage for its site.
func (s *Service) UpdateBlock(ctx context.Context, orgID, blockID uuid.UUID, expectedVersion int, d Doc) (Block, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteManage); err != nil {
		return Block{}, err
	}
	var out Block
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanBlock(tx.QueryRow(ctx, `SELECT `+blockColumns+` FROM block WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, blockID))
		if err != nil {
			return err
		}
		if err := requireSiteErr(ctx, tx, PermSiteManage, before.SiteID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		next := before
		if err := applyBlock(&next, d, false); err != nil {
			return err
		}
		diff, err := audit.Diff("block", blockAudit{before.Name, before.Hidden, before.SortOrder}, blockAudit{next.Name, next.Hidden, next.SortOrder})
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		out, err = scanBlock(tx.QueryRow(ctx, `UPDATE block SET name = $3, hidden = $4, sort_order = $5, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2 RETURNING `+blockColumns, orgID, blockID, next.Name, next.Hidden, next.SortOrder))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "block.update", TargetType: "block", TargetID: blockID, Diff: diff})
	})
	if errors.Is(err, ErrVersion) {
		return Block{}, VersionConflict("block")
	}
	if err != nil {
		return Block{}, DBProblem(err, "update block")
	}
	return out, nil
}
