package people

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/extval"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// MaxImportRows bounds one import.
const MaxImportRows = 5000

// ImportColumns are the canonical columns of the units and people import, in
// the order of the template. Any may be mapped from a differently named
// column of the file with the request's mapping.
var ImportColumns = []string{
	"block", "door_number", "legal_ref", "type", "area_m2", "arsa_payi",
	"owner_name", "owner_phone", "owner_email", "owner_share_pct", "owner_resides",
	"tenant_name", "tenant_phone", "tenant_email", "plates",
	"opening_balance", "opening_balance_side", "opening_balance_date",
}

// importAliases are header names recognized without a mapping (compared lower
// case, ASCII-folded, with spaces and dashes as underscores).
var importAliases = map[string][]string{
	"block":                {"blok", "building", "bina"},
	"door_number":          {"door", "unit", "no", "daire", "daire_no", "kapi_no", "bagimsiz_bolum_no_kapi"},
	"legal_ref":            {"tapu", "tapu_no", "bagimsiz_bolum_no", "legal"},
	"type":                 {"tur", "tip", "unit_type"},
	"area_m2":              {"area", "m2", "alan", "brut_alan"},
	"arsa_payi":            {"arsa", "arsa_payi", "land_share", "share"},
	"owner_name":           {"owner", "malik", "kat_maliki", "mal_sahibi", "owner_full_name"},
	"owner_phone":          {"malik_telefon", "owner_tel", "owner_gsm"},
	"owner_email":          {"malik_eposta", "owner_mail"},
	"owner_share_pct":      {"hisse", "hisse_yuzde", "owner_share"},
	"owner_resides":        {"oturuyor", "resides"},
	"tenant_name":          {"tenant", "kiraci"},
	"tenant_phone":         {"kiraci_telefon", "tenant_tel", "tenant_gsm"},
	"tenant_email":         {"kiraci_eposta", "tenant_mail"},
	"plates":               {"plate", "plaka", "plakalar", "vehicles"},
	"opening_balance":      {"balance", "borc", "devir", "acilis_bakiyesi", "opening"},
	"opening_balance_side": {"balance_side"},
	"opening_balance_date": {"balance_date", "bakiye_tarihi"},
}

var foldASCII = strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "i̇", "i", "ö", "o", "ş", "s", "ü", "u")

func foldHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, string(rune(0xFEFF)))))
	s = foldASCII.Replace(strings.NewReplacer("Ç", "c", "Ğ", "g", "İ", "i", "I", "i", "Ö", "o", "Ş", "s", "Ü", "u").Replace(s))
	return strings.NewReplacer(" ", "_", "-", "_", ".", "_").Replace(s)
}

// ImportRequest is one import call.
type ImportRequest struct {
	Filename string
	Data     []byte
	DryRun   bool
	// Mapping maps a canonical column to the file's header text.
	Mapping map[string]string
}

// ImportMessage is a finding about a row.
type ImportMessage struct {
	Level  string         `json:"level"` // error or warning
	Column string         `json:"column"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// ImportRowReport is the outcome of one data row.
type ImportRowReport struct {
	Row        int             `json:"row"`
	DoorNumber string          `json:"door_number"`
	Status     string          `json:"status"` // ok, warning, error
	Action     string          `json:"action"` // create, update, unchanged, skipped
	Messages   []ImportMessage `json:"messages"`
}

// ImportReport is the row-level report of an import (also stored with the batch).
type ImportReport struct {
	BatchID uuid.UUID         `json:"batch_id"`
	DryRun  bool              `json:"dry_run"`
	Applied bool              `json:"applied"`
	Status  string            `json:"status"` // dry_run, applied or failed
	Summary map[string]int    `json:"summary"`
	Columns map[string]string `json:"columns"` // canonical column to the header it was read from
	Rows    []ImportRowReport `json:"rows"`
}

// ParseSheet reads the rows of a CSV (comma, semicolon or tab separated; UTF-8
// with or without BOM) or XLSX (first sheet) file.
func ParseSheet(filename string, data []byte) ([][]string, error) {
	if strings.EqualFold(filepath.Ext(filename), ".xlsx") {
		f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 32 << 20})
		if err != nil {
			return nil, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
		}
		defer f.Close()
		sheets := f.GetSheetList()
		if len(sheets) == 0 {
			return nil, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
		}
		rows, err := f.GetRows(sheets[0])
		if err != nil {
			return nil, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
		}
		return rows, nil
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	first, _, _ := bytes.Cut(data, []byte("\n"))
	delim := ','
	for _, c := range []rune{';', '\t'} {
		if bytes.Count(first, []byte(string(c))) > bytes.Count(first, []byte(string(delim))) {
			delim = c
		}
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var rows [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
		}
		rows = append(rows, rec)
	}
	return rows, nil
}

// MapColumns resolves each canonical column to the index of a header: the
// explicit mapping first, then the canonical name and its aliases.
func MapColumns(header []string, mapping map[string]string) (idx map[string]int, used map[string]string, err error) {
	idx, used = map[string]int{}, map[string]string{}
	folded := make([]string, len(header))
	for i, h := range header {
		folded[i] = foldHeader(h)
	}
	for col, want := range mapping {
		if !slices.Contains(ImportColumns, col) {
			return nil, nil, problem.Validation(problem.Field{Field: "mapping." + col, Code: "unknown_field"})
		}
		i := slices.Index(folded, foldHeader(want))
		if i < 0 {
			return nil, nil, problem.Validation(problem.Field{Field: "mapping." + col, Code: "invalid_value", Params: map[string]any{"header": want}})
		}
		idx[col], used[col] = i, header[i]
	}
	for _, col := range ImportColumns {
		if _, done := idx[col]; done {
			continue
		}
		cands := append([]string{col}, importAliases[col]...)
		for _, c := range cands {
			if i := slices.Index(folded, c); i >= 0 {
				idx[col], used[col] = i, header[i]
				break
			}
		}
	}
	if _, ok := idx["door_number"]; !ok {
		return nil, nil, problem.Validation(problem.Field{Field: "file", Code: "required", Params: map[string]any{"column": "door_number"}})
	}
	return idx, used, nil
}

// ParseMoney reads an amount such as "1250,50", "1.250,50", "-300" or "1,250.50"
// into minor units.
func ParseMoney(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, " ", ""))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "TL"), "TRY")
	if s == "" {
		return 0, false
	}
	lc, ld := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case lc >= 0 && ld >= 0:
		if lc > ld {
			s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case lc >= 0:
		s = strings.ReplaceAll(s, ",", ".")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e12 {
		return 0, false
	}
	return int64(math.Round(f * 100)), true
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "evet", "e", "x":
		return true, true
	case "0", "false", "no", "n", "hayir", "hayır", "h":
		return false, true
	}
	return false, false
}

type importRow struct {
	no                                  int
	block, door, legal, typ             string
	typSet                              bool
	area                                *float64
	arsa                                string
	ownerName, ownerPhone, ownerEmail   string
	coOwnerRow                          bool // the door already appeared earlier in this file: a second owner is a co-owner
	ownerShare                          *float64
	ownerResides                        bool
	tenantName, tenantPhone, tenantMail string
	plates                              []string
	opening                             map[string]any
	msgs                                []ImportMessage
}

func (r *importRow) err(col, code string, params map[string]any) {
	r.msgs = append(r.msgs, ImportMessage{Level: "error", Column: col, Code: code, Params: params})
}
func (r *importRow) warn(col, code string) {
	r.msgs = append(r.msgs, ImportMessage{Level: "warning", Column: col, Code: code})
}
func (r *importRow) failed() bool {
	for _, m := range r.msgs {
		if m.Level == "error" {
			return true
		}
	}
	return false
}

func (s *Service) parseImportRow(no int, rec []string, idx map[string]int, today time.Time) *importRow {
	get := func(col string) string {
		if i, ok := idx[col]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	r := &importRow{no: no, ownerResides: true}
	r.block, r.door, r.legal, r.typ = get("block"), get("door_number"), get("legal_ref"), strings.ToLower(get("type"))
	if r.door == "" {
		r.err("door_number", "required", nil)
	} else if len([]rune(r.door)) > 40 {
		r.err("door_number", "too_long", map[string]any{"max": 40})
	} else if tenancy.HasFormulaPrefix(r.door) {
		r.err("door_number", "invalid_value", map[string]any{"reason": "formula_prefix"})
	}
	r.typSet = r.typ != ""
	if r.typ == "" {
		r.typ = "flat"
	} else if !slices.Contains(tenancy.UnitTypes, r.typ) {
		r.err("type", "invalid_value", nil)
	}
	if v := get("area_m2"); v != "" {
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil || f <= 0 || f > 1e6 {
			r.err("area_m2", "invalid_value", nil)
		} else {
			r.area = &f
		}
	}
	if v := get("arsa_payi"); v != "" {
		if _, ok := tenancy.ParseWeight(v); !ok {
			r.err("arsa_payi", "invalid_format", nil)
		}
		r.arsa = v
	}
	r.ownerName, r.tenantName = get("owner_name"), get("tenant_name")
	for _, c := range []struct {
		col  string
		dst  *string
		kind string
	}{{"owner_phone", &r.ownerPhone, "phone"}, {"tenant_phone", &r.tenantPhone, "phone"}, {"owner_email", &r.ownerEmail, "email"}, {"tenant_email", &r.tenantMail, "email"}} {
		v := get(c.col)
		if v == "" {
			continue
		}
		if c.kind == "phone" {
			if p, ok := identity.NormalizePhone(v, s.cfg.DefaultCountryCode); ok {
				*c.dst = p
			} else {
				r.err(c.col, "invalid_format", nil)
			}
		} else if e, ok := identity.NormalizeEmail(v); ok {
			*c.dst = e
		} else {
			r.err(c.col, "invalid_format", nil)
		}
	}
	if (r.ownerPhone != "" || r.ownerEmail != "") && r.ownerName == "" {
		r.err("owner_name", "required", nil)
	}
	if (r.tenantPhone != "" || r.tenantMail != "") && r.tenantName == "" {
		r.err("tenant_name", "required", nil)
	}
	if v := get("owner_share_pct"); v != "" {
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.ReplaceAll(v, ",", "."), "%"), 64)
		if err != nil || f <= 0 || f > 100 {
			r.err("owner_share_pct", "invalid_value", nil)
		} else {
			r.ownerShare = &f
		}
	}
	if v := get("owner_resides"); v != "" {
		if b, ok := parseBool(v); ok {
			r.ownerResides = b
		} else {
			r.err("owner_resides", "invalid_value", nil)
		}
	}
	for _, p := range strings.FieldsFunc(get("plates"), func(c rune) bool { return c == ',' || c == ';' || c == '/' }) {
		if n, ok := tenancy.NormalizePlate(p); ok {
			r.plates = append(r.plates, n)
		} else {
			r.err("plates", "invalid_format", map[string]any{"value": p})
		}
	}
	if v := get("opening_balance"); v != "" {
		minor, ok := ParseMoney(v)
		if !ok {
			r.err("opening_balance", "invalid_format", nil)
		} else {
			side := strings.ToLower(get("opening_balance_side"))
			if side == "" {
				side = SideOwner
			}
			if side != SideOwner && side != SideTenant {
				r.err("opening_balance_side", "invalid_value", nil)
			}
			asOf := today.Format(dateLayout)
			if d := get("opening_balance_date"); d != "" {
				t, err := time.Parse(dateLayout, d)
				if err != nil {
					if t2, err2 := time.Parse("02.01.2006", d); err2 == nil {
						t, err = t2, nil
					}
				}
				if err != nil {
					r.err("opening_balance_date", "invalid_format", nil)
				} else {
					asOf = t.Format(dateLayout)
				}
			}
			r.opening = map[string]any{"amount_minor": minor, "side": side, "as_of": asOf}
			if side == SideTenant && r.tenantName == "" {
				r.warn("opening_balance_side", "no_tenant_in_row")
			}
		}
	}
	return r
}

// importState carries what rows of one file share.
type importState struct {
	s         *Service
	orgID     uuid.UUID
	site      tenancy.Site
	blocks    map[string]uuid.UUID
	soleBlock *uuid.UUID
	doors     map[string]uuid.UUID // doors seen in this file
	arsaKey   *uuid.UUID
	sum       map[string]int
	// replaceOwner (mapping key replace_owner = true) lets a different named owner in the file get a new owner occupancy
	replaceOwner bool
}

func (st *importState) count(k string) { st.sum[k]++ }

// applyRow upserts one row inside tx. It returns the action and the unit.
func (st *importState) applyRow(ctx context.Context, tx pgx.Tx, r *importRow) (string, uuid.UUID, error) {
	orgID, siteID := st.orgID, st.site.ID
	// block
	var blockID uuid.UUID
	if r.block == "" {
		if st.soleBlock == nil {
			r.err("block", "required", nil)
			return "skipped", uuid.Nil, nil
		}
		blockID = *st.soleBlock
	} else if b, ok := st.blocks[r.block]; ok {
		blockID = b
	} else {
		blockID = id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO block (id, org_id, site_id, name, sort_order) VALUES ($1, $2, $3, $4, $5)`, blockID, orgID, siteID, r.block, len(st.blocks)); err != nil {
			return "", uuid.Nil, err
		}
		st.blocks[r.block] = blockID
		st.count("blocks_created")
	}
	action := "unchanged"
	var unit tenancy.Unit
	var uid uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM unit WHERE org_id = $1 AND site_id = $2 AND door_number = $3`, orgID, siteID, r.door).Scan(&uid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		uid = id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO unit (id, org_id, site_id, block_id, door_number, legal_ref, type, area_m2) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uid, orgID, siteID, blockID, r.door, nullStr(r.legal), r.typ, r.area); err != nil {
			return "", uuid.Nil, err
		}
		action = "create"
		st.count("units_created")
	case err != nil:
		return "", uuid.Nil, err
	default:
		_, dup := st.doors[r.door]
		r.coOwnerRow = dup
		cur, err := tenancy.GetUnitRow(ctx, tx, orgID, uid)
		if err != nil {
			return "", uuid.Nil, err
		}
		changed := (r.legal != "" && (cur.LegalRef == nil || *cur.LegalRef != r.legal)) || (r.typSet && cur.Type != r.typ) ||
			(r.area != nil && (cur.AreaM2 == nil || *cur.AreaM2 != *r.area)) || (r.block != "" && cur.BlockID != blockID)
		if dup && changed {
			r.warn("door_number", "conflicting_unit_data")
		} else if changed {
			if _, err := tx.Exec(ctx, `UPDATE unit SET block_id = $3, legal_ref = COALESCE($4, legal_ref), type = $5,
				area_m2 = COALESCE($6, area_m2), updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
				orgID, uid, blockID, nullStr(r.legal), map[bool]string{true: r.typ, false: cur.Type}[r.typSet], r.area); err != nil {
				return "", uuid.Nil, err
			}
			action = "update"
			st.count("units_updated")
		}
	}
	st.doors[r.door] = uid
	unit, err = tenancy.GetUnitRow(ctx, tx, orgID, uid)
	if err != nil {
		return "", uuid.Nil, err
	}
	if r.arsa != "" && st.arsaKey != nil {
		w, _ := tenancy.ParseWeight(r.arsa)
		tag, err := tx.Exec(ctx, `INSERT INTO allocation_key_value (id, org_id, site_id, key_id, unit_id, value, raw) VALUES ($1, $2, $3, $4, $5, $6::numeric, $7)
			ON CONFLICT (org_id, key_id, unit_id) DO UPDATE SET value = $6::numeric, raw = $7, updated_at = now(), version = allocation_key_value.version + 1
			WHERE allocation_key_value.raw IS DISTINCT FROM $7`, id.New(), orgID, siteID, *st.arsaKey, uid, w.FloatString(12), r.arsa)
		if err != nil {
			return "", uuid.Nil, err
		}
		if tag.RowsAffected() > 0 {
			if action == "unchanged" {
				action = "update"
			}
			st.count("shares_set")
		}
	}
	for _, role := range []struct {
		kind, name, phone, email string
	}{{KindOwner, r.ownerName, r.ownerPhone, r.ownerEmail}, {KindTenant, r.tenantName, r.tenantPhone, r.tenantMail}} {
		if role.name == "" {
			continue
		}
		created, err := st.person(ctx, tx, unit, role.kind, role.name, role.phone, role.email, r)
		if err != nil {
			return "", uuid.Nil, err
		}
		if created && action == "unchanged" {
			action = "update"
		}
	}
	for _, p := range r.plates {
		var onUnit uuid.UUID
		err := tx.QueryRow(ctx, `SELECT unit_id FROM vehicle WHERE org_id = $1 AND site_id = $2 AND plate = $3`, orgID, siteID, p).Scan(&onUnit)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `INSERT INTO vehicle (id, org_id, site_id, unit_id, plate) VALUES ($1, $2, $3, $4, $5)`, id.New(), orgID, siteID, uid, p); err != nil {
				return "", uuid.Nil, err
			}
			st.count("vehicles_created")
			if action == "unchanged" {
				action = "update"
			}
		case err != nil:
			return "", uuid.Nil, err
		case onUnit != uid:
			r.err("plates", "conflict", map[string]any{"plate": p})
		}
	}
	return action, uid, nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ImportUnits imports units and people from a spreadsheet into a site (idempotent
// by door number; re-running the same file changes nothing). With DryRun it
// computes the same report and writes only the report. Opening-balance
// columns are validated and stored with the batch for the money module. It
// needs import.run for the site.
func (s *Service) ImportUnits(ctx context.Context, orgID, siteID uuid.UUID, req ImportRequest) (ImportReport, error) {
	if err := authz.RequireAnywhere(ctx, tenancy.PermImportRun); err != nil {
		return ImportReport{}, err
	}
	rows, err := ParseSheet(req.Filename, req.Data)
	if err != nil {
		return ImportReport{}, err
	}
	if len(rows) < 2 {
		return ImportReport{}, problem.Validation(problem.Field{Field: "file", Code: "required", Params: map[string]any{"reason": "no_data_rows"}})
	}
	if len(rows)-1 > MaxImportRows {
		return ImportReport{}, problem.Validation(problem.Field{Field: "file", Code: "too_large", Params: map[string]any{"max_rows": MaxImportRows}})
	}
	mapping := map[string]string{}
	replaceOwner := false
	for k, v := range req.Mapping {
		if k == "replace_owner" {
			replaceOwner = strings.EqualFold(strings.TrimSpace(v), "true")
			continue
		}
		mapping[k] = v
	}
	idx, used, err := MapColumns(rows[0], mapping)
	if err != nil {
		return ImportReport{}, err
	}
	var report ImportReport
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireSiteScope(ctx, tx, tenancy.PermImportRun, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, true, false)
		if err != nil {
			return err
		}
		st := &importState{s: s, orgID: orgID, site: site, blocks: map[string]uuid.UUID{}, doors: map[string]uuid.UUID{}, sum: map[string]int{}, replaceOwner: replaceOwner}
		brows, err := tx.Query(ctx, `SELECT id, name FROM block WHERE org_id = $1 AND site_id = $2 ORDER BY sort_order, name`, orgID, siteID)
		if err != nil {
			return err
		}
		var all []uuid.UUID
		for brows.Next() {
			var b uuid.UUID
			var n string
			if err := brows.Scan(&b, &n); err != nil {
				brows.Close()
				return err
			}
			st.blocks[n] = b
			all = append(all, b)
		}
		brows.Close()
		if len(all) == 1 {
			st.soleBlock = &all[0]
		}
		var ak uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM allocation_key WHERE org_id = $1 AND site_id = $2 AND code = 'arsa_payi'`, orgID, siteID).Scan(&ak); err == nil {
			st.arsaKey = &ak
		}
		// everything runs in a savepoint: a dry run (or a file with errors) rolls it back
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		type parsed struct {
			row    *importRow
			unit   uuid.UUID
			action string
		}
		var done []parsed
		rep := ImportReport{DryRun: req.DryRun, Columns: used, Rows: []ImportRowReport{}}
		for i, rec := range rows[1:] {
			if allBlank(rec) {
				continue
			}
			r := s.parseImportRow(i+2, rec, idx, s.today())
			// the plate validators of the packs enabled for the organization (Turkish plate format)
			for _, p := range r.plates {
				if f := extval.Check(ctx, s.validators, tx, orgID, extval.KindPlate, "plates", p); f != nil {
					params := map[string]any{"value": p}
					for k, v := range f.Params {
						params[k] = v
					}
					r.err("plates", f.Code, params)
				}
			}
			action, uid := "skipped", uuid.Nil
			if !r.failed() {
				rsp, err := sp.Begin(ctx)
				if err != nil {
					_ = sp.Rollback(ctx)
					return err
				}
				a, u, err := st.applyRow(ctx, rsp, r)
				if err != nil || r.failed() {
					_ = rsp.Rollback(ctx)
					if err != nil {
						var pe *problem.Error
						if errors.As(err, &pe) {
							code := string(pe.Code)
							if len(pe.Fields) > 0 {
								code = pe.Fields[0].Code
							}
							field := ""
							if len(pe.Fields) > 0 {
								field = pe.Fields[0].Field
							}
							r.err(field, code, nil)
						} else if tenancy.IsUnique(err, "") {
							r.err("door_number", "conflict", nil)
						} else {
							_ = sp.Rollback(ctx)
							return err
						}
					}
				} else {
					if err := rsp.Commit(ctx); err != nil {
						_ = sp.Rollback(ctx)
						return err
					}
					action, uid = a, u
				}
			}
			status := "ok"
			if r.failed() {
				status, action = "error", "skipped"
				st.count("rows_error")
			} else if len(r.msgs) > 0 {
				status = "warning"
				st.count("rows_warning")
			} else {
				st.count("rows_ok")
			}
			if action == "unchanged" {
				st.count("rows_unchanged")
			}
			msgs := r.msgs
			if msgs == nil {
				msgs = []ImportMessage{}
			}
			rep.Rows = append(rep.Rows, ImportRowReport{Row: r.no, DoorNumber: r.door, Status: status, Action: action, Messages: msgs})
			done = append(done, parsed{r, uid, action})
		}
		hasErrors := st.sum["rows_error"] > 0
		rep.Summary = st.sum
		rep.Summary["rows_total"] = len(rep.Rows)
		switch {
		case req.DryRun || hasErrors:
			if err := sp.Rollback(ctx); err != nil {
				return err
			}
			rep.Status = map[bool]string{true: "dry_run", false: "failed"}[req.DryRun]
		default:
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			rep.Status, rep.Applied = "applied", true
		}
		rep.BatchID = id.New()
		mapping, _ := json.Marshal(req.Mapping)
		summary, _ := json.Marshal(rep.Summary)
		repJSON, _ := json.Marshal(rep.Rows)
		var uidp *uuid.UUID
		if p := principalID(ctx); p != nil {
			uidp = p
		}
		batchStatus := rep.Status
		if _, err := tx.Exec(ctx, `INSERT INTO import_batch (id, org_id, site_id, filename, mapping, dry_run, status, summary, report, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, rep.BatchID, orgID, siteID, clipName(req.Filename), mapping, req.DryRun, batchStatus, summary, repJSON, uidp); err != nil {
			return err
		}
		if rep.Applied {
			for _, d := range done {
				data, _ := json.Marshal(map[string]any{"block": d.row.block, "door_number": d.row.door, "legal_ref": d.row.legal, "type": d.row.typ, "area_m2": d.row.area, "arsa_payi": d.row.arsa})
				var opening []byte
				if d.row.opening != nil {
					opening, _ = json.Marshal(d.row.opening)
				}
				status := "ok"
				if len(d.row.msgs) > 0 {
					status = "warning"
				}
				if _, err := tx.Exec(ctx, `INSERT INTO import_row (id, org_id, site_id, batch_id, row_no, status, data, opening, unit_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
					id.New(), orgID, siteID, rep.BatchID, d.row.no, status, data, opening, d.unit); err != nil {
					return err
				}
				if d.action == "create" || d.action == "update" {
					rowDiff := audit.Changes{"row": audit.Change{After: json.RawMessage(strconv.Itoa(d.row.no))}, "batch_id": audit.Change{After: json.RawMessage(strconv.Quote(rep.BatchID.String()))}}
					if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "unit.import_" + d.action, TargetType: "unit", TargetID: d.unit, Diff: rowDiff}); err != nil {
						return err
					}
				}
			}
			diff := audit.Changes{}
			for k, v := range rep.Summary {
				if strings.HasSuffix(k, "_created") || strings.HasSuffix(k, "_updated") {
					diff[k] = audit.Change{After: json.RawMessage(strconv.Itoa(v))}
				}
			}
			if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "site.import_units", TargetType: "import_batch", TargetID: rep.BatchID, Diff: diff}); err != nil {
				return err
			}
		}
		report = rep
		return nil
	})
	if err != nil {
		return ImportReport{}, tenancy.DBProblem(err, "import")
	}
	return report, nil
}

func allBlank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func clipName(s string) string {
	s = filepath.Base(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
