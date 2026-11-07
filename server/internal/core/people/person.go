package people

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ValidTCKN checks the Turkish national identity number (TC Kimlik No): 11
// digits, the first not zero, and the two check digits.
func ValidTCKN(s string) bool {
	if len(s) != 11 || s[0] == '0' {
		return false
	}
	var d [11]int
	for i := 0; i < 11; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		d[i] = int(s[i] - '0')
	}
	odd := d[0] + d[2] + d[4] + d[6] + d[8]
	even := d[1] + d[3] + d[5] + d[7]
	c10 := ((odd*7-even)%10 + 10) % 10
	if c10 != d[9] {
		return false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		sum += d[i]
	}
	return sum%10 == d[10]
}

var genericNationalID = regexp.MustCompile(`^[0-9A-Za-z]{4,30}$`)

// ValidateNationalID checks an identity number for a country: the checksum
// for TR, a plausible alphanumeric string elsewhere.
func ValidateNationalID(country, number string) bool {
	if country == "TR" {
		return ValidTCKN(number)
	}
	return genericNationalID.MatchString(number)
}

// MaskNationalID shows only the last four characters: *******1234.
func MaskNationalID(n string) string {
	if len(n) <= 4 {
		return strings.Repeat("*", len(n))
	}
	return strings.Repeat("*", len(n)-4) + n[len(n)-4:]
}

// Person is a person as the API shows it. The national id is always masked.
type Person struct {
	ID                uuid.UUID  `json:"id"`
	UserID            *uuid.UUID `json:"user_id"`
	DisplayName       string     `json:"display_name"`
	Email             *string    `json:"email"`
	Phone             *string    `json:"phone"`
	GateContactPhone  *string    `json:"gate_contact_phone"`
	NationalID        *string    `json:"national_id"`
	NationalIDCountry *string    `json:"national_id_country"`
	Notes             *string    `json:"notes"`
	SiteID            *uuid.UUID `json:"site_id"`
	HasAccount        bool       `json:"has_account"`
	Version           int        `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// personRow is a person with the sealed identity number, inside the module.
type personRow struct {
	Person
	sealed []byte
}

const personColumns = `p.id, p.user_id, p.display_name, p.contact->>'email', p.contact->>'phone', p.gate_contact_phone, p.national_id_enc,
	p.national_id_country, p.notes, p.site_id, p.user_id IS NOT NULL, p.version, p.created_at, p.updated_at`

func scanPersonRow(row pgx.Row) (personRow, error) {
	var p personRow
	err := row.Scan(&p.ID, &p.UserID, &p.DisplayName, &p.Email, &p.Phone, &p.GateContactPhone, &p.sealed,
		&p.NationalIDCountry, &p.Notes, &p.SiteID, &p.HasAccount, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

// open decrypts the identity number to mask it; a failure hides it.
func (s *Service) finish(p personRow) Person {
	out := p.Person
	if len(p.sealed) > 0 && s.cipher != nil {
		if pt, err := s.cipher.Open(p.sealed, p.ID[:]); err == nil {
			m := MaskNationalID(string(pt))
			out.NationalID = &m
		} else {
			s.log.Error("cannot open a national id", "person", p.ID, "error", err)
		}
	}
	return out
}

func (s *Service) loadPerson(ctx context.Context, tx pgx.Tx, orgID, personID uuid.UUID, forUpdate bool) (personRow, error) {
	q := `SELECT ` + personColumns + ` FROM person p WHERE p.org_id = $1 AND p.id = $2`
	if forUpdate {
		q += ` FOR UPDATE OF p`
	}
	p, err := scanPersonRow(tx.QueryRow(ctx, q, orgID, personID))
	if errors.Is(err, pgx.ErrNoRows) {
		return personRow{}, problem.NotFound("no such person")
	}
	return p, err
}

// canSee reports whether the caller holds perm for the person: organization-wide,
// or for a site the person is registered at or tied to (occupancy, membership,
// invitation or claim). The sites come from the caller's grants.
func canSee(ctx context.Context, tx pgx.Tx, orgID, personID uuid.UUID, perm authz.Permission) (bool, error) {
	orgWide, sites := siteGrants(ctx, perm)
	if orgWide || authz.Has(ctx, perm, authz.Scope{OrgID: orgID}) {
		return true, nil
	}
	if len(sites) == 0 {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM person p WHERE p.org_id = $1 AND p.id = $2 AND p.site_id = ANY($3)
		UNION ALL SELECT 1 FROM occupancy o WHERE o.org_id = $1 AND o.person_id = $2 AND o.site_id = ANY($3)
		UNION ALL SELECT 1 FROM unit_claim c WHERE c.org_id = $1 AND c.person_id = $2 AND c.site_id = ANY($3)
		UNION ALL SELECT 1 FROM invitation i WHERE i.org_id = $1 AND i.person_id = $2 AND i.site_id = ANY($3)
		UNION ALL SELECT 1 FROM membership m WHERE m.org_id = $1 AND m.person_id = $2 AND m.scope_type = 'site' AND m.scope_id = ANY($3))`,
		orgID, personID, sites).Scan(&ok)
	return ok, err
}

// ListPersons lists people, optionally filtered by a name, phone or email
// fragment or by site. It needs person.read (organization-wide, or for the
// sites the people are tied to).
func (s *Service) ListPersons(ctx context.Context, orgID uuid.UUID, q string, siteID *uuid.UUID, p tenancy.PageParams) ([]Person, string, error) {
	if err := authz.RequireAnywhere(ctx, PermPersonRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	orgWide, sites := siteGrants(ctx, PermPersonRead)
	qq := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(strings.TrimSpace(q))) + "%"
	var out []Person
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if siteID != nil {
			if _, err := authz.ScopeOfSite(ctx, tx, *siteID); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT `+personColumns+` FROM person p WHERE p.org_id = $1 AND p.id > $2
			AND ($3 OR p.site_id = ANY($4) OR EXISTS (SELECT 1 FROM occupancy o WHERE o.org_id = p.org_id AND o.person_id = p.id AND o.site_id = ANY($4)))
			AND ($5::uuid IS NULL OR p.site_id = $5 OR EXISTS (SELECT 1 FROM occupancy o WHERE o.org_id = p.org_id AND o.person_id = p.id AND o.site_id = $5))
			AND ($6 = '%%' OR lower(p.display_name) LIKE $6 OR lower(p.contact->>'phone') LIKE $6 OR lower(p.contact->>'email') LIKE $6)
			ORDER BY p.id LIMIT $7`, orgID, after, orgWide, sites, siteID, qq, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			pr, err := scanPersonRow(rows)
			if err != nil {
				return err
			}
			out = append(out, s.finish(pr))
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list persons")
	}
	items, next := tenancy.Page(out, limit, func(x Person) uuid.UUID { return x.ID })
	return items, next, nil
}

// GetPerson returns a person (national id masked). It needs person.read for them.
func (s *Service) GetPerson(ctx context.Context, orgID, personID uuid.UUID) (Person, error) {
	if err := authz.RequireAnywhere(ctx, PermPersonRead); err != nil {
		return Person{}, err
	}
	var out Person
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		pr, err := s.loadPerson(ctx, tx, orgID, personID, false)
		if err != nil {
			return err
		}
		ok, err := canSee(ctx, tx, orgID, personID, PermPersonRead)
		if err != nil {
			return err
		}
		if !ok {
			return problem.Forbidden("you do not have the person.read permission for this person")
		}
		out = s.finish(pr)
		return nil
	})
	if err != nil {
		return Person{}, tenancy.DBProblem(err, "get person")
	}
	return out, nil
}

// personInput is the validated content of a create or update.
type personInput struct {
	displayName    string
	email, phone   *string // normalized
	gatePhone      *string
	nationalID     *string
	nationalIDSet  bool // the field was sent (null clears)
	country        *string
	notes          *string
	hasEmail       bool
	hasPhone       bool
	hasGate        bool
	hasNotes       bool
	hasDisplayName bool
}

var personFields = []string{"display_name", "email", "phone", "gate_contact_phone", "national_id", "national_id_country", "notes"}

func (s *Service) parsePerson(d Doc, create bool, orgCountry string, allowed ...string) (personInput, error) {
	fields := d.Unknown(append(personFields, allowed...)...)
	var in personInput
	if d.Has("display_name") {
		v, err := d.String("display_name")
		if err != nil {
			return in, err
		}
		if v == nil || len([]rune(strings.TrimSpace(*v))) < 1 || len([]rune(*v)) > 200 {
			fields = append(fields, problem.Field{Field: "display_name", Code: "invalid_value"})
		} else {
			in.displayName, in.hasDisplayName = strings.TrimSpace(*v), true
		}
	} else if create {
		fields = append(fields, problem.Field{Field: "display_name", Code: "required"})
	}
	if d.Has("email") {
		in.hasEmail = true
		v, err := d.String("email")
		if err != nil {
			return in, err
		}
		if v != nil {
			if e, ok := identity.NormalizeEmail(*v); ok {
				in.email = &e
			} else {
				fields = append(fields, problem.Field{Field: "email", Code: "invalid_format"})
			}
		}
	}
	for _, k := range []struct {
		name string
		dst  **string
		has  *bool
	}{{"phone", &in.phone, &in.hasPhone}, {"gate_contact_phone", &in.gatePhone, &in.hasGate}} {
		if !d.Has(k.name) {
			continue
		}
		*k.has = true
		v, err := d.String(k.name)
		if err != nil {
			return in, err
		}
		if v != nil {
			if ph, ok := identity.NormalizePhone(*v, s.cfg.DefaultCountryCode); ok {
				*k.dst = &ph
			} else {
				fields = append(fields, problem.Field{Field: k.name, Code: "invalid_format"})
			}
		}
	}
	if d.Has("notes") {
		in.hasNotes = true
		v, err := d.String("notes")
		if err != nil {
			return in, err
		}
		if v != nil && len(*v) > 4000 {
			fields = append(fields, problem.Field{Field: "notes", Code: "too_long", Params: map[string]any{"max": 4000}})
		}
		in.notes = v
	}
	if d.Has("national_id_country") {
		v, err := d.String("national_id_country")
		if err != nil {
			return in, err
		}
		if v != nil && !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(*v) {
			fields = append(fields, problem.Field{Field: "national_id_country", Code: "invalid_format"})
		}
		in.country = v
	}
	if d.Has("national_id") {
		in.nationalIDSet = true
		v, err := d.String("national_id")
		if err != nil {
			return in, err
		}
		if v != nil {
			n := strings.ReplaceAll(strings.TrimSpace(*v), " ", "")
			country := orgCountry
			if in.country != nil {
				country = *in.country
			}
			if !ValidateNationalID(country, n) {
				fields = append(fields, problem.Field{Field: "national_id", Code: "invalid_value", Params: map[string]any{"country": country}})
			}
			in.nationalID = &n
			if in.country == nil {
				in.country = &country
			}
		}
	}
	if len(fields) > 0 {
		return in, problem.Validation(fields...)
	}
	return in, nil
}

func contactJSON(email, phone *string) []byte {
	m := map[string]string{}
	if email != nil {
		m["email"] = *email
	}
	if phone != nil {
		m["phone"] = *phone
	}
	b, _ := json.Marshal(m)
	return b
}

func (s *Service) seal(personID uuid.UUID, number string) ([]byte, error) {
	if s.cipher == nil {
		return nil, problem.Internal(errors.New("no ENCRYPTION_KEY configured; national ids cannot be stored"))
	}
	return s.cipher.Seal([]byte(number), personID[:])
}

// personAudit is what a person change records: PII appears hashed, and the
// identity number never appears (only that it changed).
func personAudit(p Person, idChanged bool) map[string]any {
	m := map[string]any{
		"display_name": p.DisplayName, "contact": map[string]any{"email": p.Email, "phone": p.Phone},
		"gate_contact_phone": p.GateContactPhone, "notes": p.Notes, "national_id_changed": idChanged,
	}
	return m
}

// CreatePersonTx inserts a person inside tx for module-internal callers
// (import, invitations, claims). siteID is the registering site.
func (s *Service) createPersonTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, siteID *uuid.UUID, userID *uuid.UUID, in personInput) (Person, error) {
	pid := id.New()
	var sealed []byte
	var err error
	if in.nationalID != nil {
		if sealed, err = s.seal(pid, *in.nationalID); err != nil {
			return Person{}, err
		}
	}
	pr, err := scanPersonRow(tx.QueryRow(ctx, `
		INSERT INTO person AS p (id, org_id, user_id, display_name, contact, gate_contact_phone, national_id_enc, national_id_country, notes, site_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+personColumns,
		pid, orgID, userID, in.displayName, contactJSON(in.email, in.phone), in.gatePhone, sealed, in.country, in.notes, siteID))
	if err != nil {
		return Person{}, err
	}
	out := s.finish(pr)
	diff, err := audit.Diff("person", nil, personAudit(out, in.nationalID != nil))
	if err != nil {
		return Person{}, err
	}
	if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: siteID, Action: "person.create", TargetType: "person", TargetID: pid, Diff: diff}); err != nil {
		return Person{}, err
	}
	return out, nil
}

// CreatePerson adds a person. It needs person.manage, organization-wide or,
// when site_id names a site, for that site (the person is then registered there).
func (s *Service) CreatePerson(ctx context.Context, orgID uuid.UUID, d Doc) (Person, error) {
	if err := authz.RequireAnywhere(ctx, PermPersonManage); err != nil {
		return Person{}, err
	}
	var siteID *uuid.UUID
	if d.Has("site_id") && !d.IsNull("site_id") {
		var v uuid.UUID
		if err := d.Get("site_id", &v); err != nil {
			return Person{}, err
		}
		siteID = &v
	}
	var out Person
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var country string
		if err := tx.QueryRow(ctx, `SELECT country_code FROM organization WHERE id = $1`, orgID).Scan(&country); err != nil {
			return err
		}
		in, err := s.parsePerson(d, true, country, "site_id")
		if err != nil {
			return err
		}
		if siteID != nil {
			if _, err := requireSiteScope(ctx, tx, PermPersonManage, *siteID); err != nil {
				return err
			}
			if _, err := tenancy.LoadSite(ctx, tx, orgID, *siteID, false, false); err != nil {
				return err
			}
		} else if err := authz.Require(ctx, PermPersonManage, authz.Scope{OrgID: orgID}); err != nil {
			return err
		}
		out, err = s.createPersonTx(ctx, tx, orgID, siteID, nil, in)
		return err
	})
	if err != nil {
		return Person{}, tenancy.DBProblem(err, "person")
	}
	return out, nil
}

func requireSiteScope(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) (authz.Scope, error) {
	sc, err := authz.ScopeOfSite(ctx, tx, siteID)
	if err != nil {
		return sc, err
	}
	return sc, authz.Require(ctx, perm, sc)
}

// UpdatePerson applies d if the person is still at expectedVersion. It needs
// person.manage for the person.
func (s *Service) UpdatePerson(ctx context.Context, orgID, personID uuid.UUID, expectedVersion int, d Doc) (Person, error) {
	if err := authz.RequireAnywhere(ctx, PermPersonManage); err != nil {
		return Person{}, err
	}
	var out Person
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		pr, err := s.loadPerson(ctx, tx, orgID, personID, true)
		if err != nil {
			return err
		}
		ok, err := canSee(ctx, tx, orgID, personID, PermPersonManage)
		if err != nil {
			return err
		}
		if !ok {
			return problem.Forbidden("you do not have the person.manage permission for this person")
		}
		if pr.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		var country string
		if err := tx.QueryRow(ctx, `SELECT country_code FROM organization WHERE id = $1`, orgID).Scan(&country); err != nil {
			return err
		}
		in, err := s.parsePerson(d, false, country)
		if err != nil {
			return err
		}
		before := s.finish(pr)
		next := before
		if in.hasDisplayName {
			next.DisplayName = in.displayName
		}
		if in.hasEmail {
			next.Email = in.email
		}
		if in.hasPhone {
			next.Phone = in.phone
		}
		if in.hasGate {
			next.GateContactPhone = in.gatePhone
		}
		if in.hasNotes {
			next.Notes = in.notes
		}
		sealed, country2 := pr.sealed, before.NationalIDCountry
		if in.nationalIDSet {
			if in.nationalID == nil {
				sealed, country2 = nil, nil
			} else {
				if sealed, err = s.seal(personID, *in.nationalID); err != nil {
					return err
				}
				country2 = in.country
			}
		} else if in.country != nil && sealed != nil {
			return problem.Validation(problem.Field{Field: "national_id_country", Code: "invalid_value", Params: map[string]any{"reason": "send national_id with it"}})
		}
		diff, err := audit.Diff("person", personAudit(before, false), personAudit(next, in.nationalIDSet))
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		upd, err := scanPersonRow(tx.QueryRow(ctx, `
			UPDATE person AS p SET display_name = $3, contact = $4, gate_contact_phone = $5, notes = $6, national_id_enc = $7, national_id_country = $8,
			       updated_at = now(), version = version + 1
			WHERE p.org_id = $1 AND p.id = $2 RETURNING `+personColumns,
			orgID, personID, next.DisplayName, contactJSON(next.Email, next.Phone), next.GateContactPhone, next.Notes, sealed, country2))
		if err != nil {
			return err
		}
		out = s.finish(upd)
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: pr.SiteID, Action: "person.update", TargetType: "person", TargetID: personID, Diff: diff})
	})
	if errors.Is(err, tenancy.ErrVersion) {
		return Person{}, tenancy.VersionConflict("person")
	}
	if err != nil {
		return Person{}, tenancy.DBProblem(err, "update person")
	}
	return out, nil
}
