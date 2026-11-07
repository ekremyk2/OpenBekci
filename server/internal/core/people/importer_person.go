package people

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// importMatch is a person on a unit as the importer compares them.
type importMatch struct {
	id          uuid.UUID
	name, phone string
	email       string
}

func sameImportPerson(m importMatch, name, phone, email string) bool {
	return (phone != "" && m.phone == phone) || (email != "" && m.email == email) ||
		strings.EqualFold(strings.TrimSpace(m.name), strings.TrimSpace(name))
}

// unitPeople lists the people with an occupancy of kind on the unit: the
// current ones, or the ended ones.
func (st *importState) unitPeople(ctx context.Context, tx pgx.Tx, unitID uuid.UUID, kind string, ended bool) ([]importMatch, error) {
	cond := `o.valid_from <= $4 AND (o.valid_to IS NULL OR o.valid_to > $4)`
	if ended {
		cond = `o.valid_to IS NOT NULL AND o.valid_to <= $4`
	}
	rows, err := tx.Query(ctx, `SELECT p.id, p.display_name, COALESCE(p.contact->>'phone', ''), lower(COALESCE(p.contact->>'email', ''))
		FROM occupancy o JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
		WHERE o.org_id = $1 AND o.unit_id = $2 AND o.kind = $3 AND `+cond, st.orgID, unitID, kind, st.s.today())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []importMatch
	for rows.Next() {
		var m importMatch
		if err := rows.Scan(&m.id, &m.name, &m.phone, &m.email); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// person gives the unit its owner or tenant from a row. It matches only the
// unit's own occupants and the site's own people (never the whole
// organization, so a dry run reveals nothing the caller cannot see), never
// reopens a person whose occupancy has ended (a move-out stays a move-out), and
// reports a different named occupant as a warning. With replace_owner set in the
// mapping, a different named owner is added as a new owner occupancy.
func (st *importState) person(ctx context.Context, tx pgx.Tx, unit tenancy.Unit, kind, name, phone, email string, r *importRow) (bool, error) {
	s, orgID := st.s, st.orgID
	column := kind + "_name"
	current, err := st.unitPeople(ctx, tx, unit.ID, kind, false)
	if err != nil {
		return false, err
	}
	for _, m := range current {
		if sameImportPerson(m, name, phone, email) {
			return false, nil // already there: re-importing the file changes nothing
		}
	}
	if len(current) > 0 && !(kind == KindOwner && (st.replaceOwner || r.coOwnerRow)) {
		r.warn(column, "unit_has_different_"+kind)
		return false, nil
	}
	former, err := st.unitPeople(ctx, tx, unit.ID, kind, true)
	if err != nil {
		return false, err
	}
	for _, m := range former {
		if sameImportPerson(m, name, phone, email) {
			r.warn(column, "former_occupant")
			return false, nil
		}
	}
	var pid uuid.UUID
	err = pgx.ErrNoRows
	if phone != "" || email != "" {
		err = tx.QueryRow(ctx, `SELECT p.id FROM person p WHERE p.org_id = $1
			AND (p.site_id = $2 OR EXISTS (SELECT 1 FROM occupancy o WHERE o.org_id = p.org_id AND o.person_id = p.id AND o.site_id = $2
				AND (o.valid_to IS NULL OR o.valid_to > $5)))
			AND (($3 <> '' AND p.contact->>'phone' = $3) OR ($4 <> '' AND lower(p.contact->>'email') = $4)) ORDER BY p.created_at LIMIT 1`,
			orgID, st.site.ID, phone, email, s.today()).Scan(&pid)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		in := personInput{displayName: name}
		if phone != "" {
			in.phone = &phone
		}
		if email != "" {
			in.email = &email
		}
		sid := st.site.ID
		p, err := s.createPersonTx(ctx, tx, orgID, &sid, nil, in)
		if err != nil {
			return false, err
		}
		pid = p.ID
		st.count("persons_created")
	} else if err != nil {
		return false, err
	}
	var corr bool
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND side = $3 AND is_correspondent AND valid_to IS NULL)`, orgID, unit.ID, kind).Scan(&corr); err != nil {
		return false, err
	}
	side := SideOwner
	resides := r.ownerResides
	if kind == KindTenant {
		side, resides = SideTenant, true
	}
	in := occInput{unitID: unit.ID, personID: pid, kind: kind, side: side, correspondent: corr, resides: resides, from: s.today()}
	if kind == KindOwner {
		in.sharePct = r.ownerShare
	}
	if _, err := s.insertOccTx(ctx, tx, orgID, unit, in); err != nil {
		return false, err
	}
	st.count("occupancies_created")
	return true, nil
}
