package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MeSite is a site the caller can see.
type MeSite struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Timezone string    `json:"timezone"`
	Currency string    `json:"currency"`
}

// MeOccupancy is one occupancy of the caller (current or former).
type MeOccupancy struct {
	ID              uuid.UUID `json:"id"`
	UnitLabel       string    `json:"unit_label"`
	OrgID           uuid.UUID `json:"org_id"`
	SiteID          uuid.UUID `json:"site_id"`
	SiteName        string    `json:"site_name"`
	UnitID          uuid.UUID `json:"unit_id"`
	DoorNumber      string    `json:"door_number"`
	BlockName       string    `json:"block_name"`
	Kind            string    `json:"kind"`
	Side            string    `json:"side"`
	IsCorrespondent bool      `json:"is_correspondent"`
	MayMoney        bool      `json:"may_see_money"`
	Resides         bool      `json:"resides"`
	ValidFrom       string    `json:"valid_from"`
	ValidTo         *string   `json:"valid_to"`
	Current         bool      `json:"current"`
}

// loadSitesAndOccupancies fills the sites each organization shows the caller
// (through an organization, site or block membership, or an occupancy) and
// the caller's occupancies, on the platform pool's transaction.
func (s *Service) loadSitesAndOccupancies(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time, me *Me) error {
	idx := map[uuid.UUID]int{}
	for i, o := range me.Organizations {
		idx[o.ID] = i
		me.Organizations[i].Sites = []MeSite{}
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT s.org_id, s.id, s.name, s.timezone, s.currency
		FROM site s
		JOIN organization o ON o.id = s.org_id AND o.status = 'active'
		JOIN person p ON p.org_id = s.org_id AND p.user_id = $1
		WHERE s.status = 'active' AND (
		    EXISTS (SELECT 1 FROM membership m WHERE m.org_id = s.org_id AND m.person_id = p.id
		            AND m.valid_from <= $2 AND (m.valid_to IS NULL OR m.valid_to > $2)
		            AND (m.scope_type = 'org' OR (m.scope_type = 'site' AND m.scope_id = s.id)
		                 OR (m.scope_type = 'block' AND m.scope_id IN (SELECT b.id FROM block b WHERE b.org_id = s.org_id AND b.site_id = s.id))))
		 OR EXISTS (SELECT 1 FROM occupancy oc WHERE oc.org_id = s.org_id AND oc.person_id = p.id AND oc.site_id = s.id))
		ORDER BY s.name, s.id`, userID, now)
	if err != nil {
		return err
	}
	for rows.Next() {
		var org uuid.UUID
		var st MeSite
		if err := rows.Scan(&org, &st.ID, &st.Name, &st.Timezone, &st.Currency); err != nil {
			rows.Close()
			return err
		}
		if i, ok := idx[org]; ok {
			me.Organizations[i].Sites = append(me.Organizations[i].Sites, st)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	orows, err := tx.Query(ctx, `
		SELECT oc.id, oc.org_id, s.id, s.name, u.id, u.door_number, b.name, oc.kind, oc.side, oc.is_correspondent, oc.may_see_money, oc.resides,
		       to_char(oc.valid_from, 'YYYY-MM-DD'), to_char(oc.valid_to, 'YYYY-MM-DD'),
		       oc.valid_from <= ($2::timestamptz AT TIME ZONE s.timezone)::date AND (oc.valid_to IS NULL OR oc.valid_to > ($2::timestamptz AT TIME ZONE s.timezone)::date)
		FROM occupancy oc
		JOIN person p ON p.org_id = oc.org_id AND p.id = oc.person_id AND p.user_id = $1
		JOIN site s ON s.org_id = oc.org_id AND s.id = oc.site_id AND s.status = 'active'
		JOIN organization o ON o.id = oc.org_id AND o.status = 'active'
		JOIN unit u ON u.org_id = oc.org_id AND u.id = oc.unit_id
		JOIN block b ON b.org_id = u.org_id AND b.id = u.block_id
		ORDER BY s.name, u.door_number, oc.valid_from, oc.id`, userID, now)
	if err != nil {
		return err
	}
	defer orows.Close()
	me.Occupancies = []MeOccupancy{}
	for orows.Next() {
		var m MeOccupancy
		if err := orows.Scan(&m.ID, &m.OrgID, &m.SiteID, &m.SiteName, &m.UnitID, &m.DoorNumber, &m.BlockName, &m.Kind, &m.Side,
			&m.IsCorrespondent, &m.MayMoney, &m.Resides, &m.ValidFrom, &m.ValidTo, &m.Current); err != nil {
			return err
		}
		m.UnitLabel = m.DoorNumber
		me.Occupancies = append(me.Occupancies, m)
	}
	return orows.Err()
}
