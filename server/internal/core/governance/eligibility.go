package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SnapUnit is one unit an owner votes for.
type SnapUnit struct {
	UnitID     uuid.UUID `json:"unit_id"`
	DoorNumber string    `json:"door_number"`
	Share      float64   `json:"share"`
}

// SnapOwner is one owner of record with what they vote for. Co-owners of a
// unit vote through one representative: the unit's correspondent, else the
// largest share, else the earliest owner.
type SnapOwner struct {
	PersonID    uuid.UUID  `json:"person_id"`
	DisplayName string     `json:"display_name"`
	Units       []SnapUnit `json:"units"`
	Votes       int64      `json:"votes"`
	// Share is the arsa payi the owner votes for (internal, exact enough for
	// the quorum); ShareText is how the API shows it, a decimal string.
	Share     float64 `json:"share_value"`
	ShareText string  `json:"share"`
	// VoteWeight is the number of votes (the attendance desk shows it next to the share).
	VoteWeight int64 `json:"vote_weight"`
	// UnitIDs and UnitLabels are every unit the person owns, including co-owned
	// units that another owner votes for; RepresentativeOfUnitIDs are the units
	// this person votes for.
	UnitIDs                 []uuid.UUID `json:"unit_ids"`
	UnitLabels              []string    `json:"unit_labels"`
	RepresentativeOfUnitIDs []uuid.UUID `json:"representative_of_unit_ids"`
}

// Snapshot is who could vote and with what weight, as of the day the session
// opened (G1); at the call it is a preview.
type Snapshot struct {
	// TakenAt is when the snapshot was computed.
	TakenAt time.Time `json:"taken_at"`
	AsOf    time.Time `json:"as_of"`
	VoteWeight string    `json:"vote_weight"`
	// ShareBasis is arsa_payi, or equal when the site has no arsa payi values.
	ShareBasis string      `json:"share_basis"`
	UnitCount  int64       `json:"unit_count"`
	TotalVotes int64       `json:"total_votes"`
	TotalShare float64     `json:"total_share"`
	Owners     []SnapOwner `json:"owners"`
	// Unrepresented are the door numbers of units without an owner of record;
	// nobody can vote for them and they are not counted.
	Unrepresented []string `json:"unrepresented_units"`
}

// Total is the snapshot's eligible weight.
func (sn Snapshot) Total() Weight { return Weight{sn.TotalVotes, sn.TotalShare} }

// Owner finds an owner of record.
func (sn Snapshot) Owner(person uuid.UUID) (SnapOwner, bool) {
	for _, o := range sn.Owners {
		if o.PersonID == person {
			return o, true
		}
	}
	return SnapOwner{}, false
}

// WeightOf is the weight of the given units of owner o (all units when none given).
func (sn Snapshot) WeightOf(o SnapOwner, units []uuid.UUID) Weight {
	var w Weight
	n := 0
	for _, u := range o.Units {
		if len(units) == 0 || slices.Contains(units, u.UnitID) {
			n++
			w.Share += u.Share
		}
	}
	switch {
	case n == 0:
		return Weight{}
	case sn.VoteWeight == VoteWeightPerOwner:
		w.Votes = 1
	default:
		w.Votes = int64(n)
	}
	return w
}

type occRow struct {
	unit, person uuid.UUID
	name         string
	correspond   bool
	sharePct     float64
	from         time.Time
	id           uuid.UUID
}

// computeSnapshot reads the owners of record of a site as of the given day.
func computeSnapshot(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, asOf time.Time, weight string) (Snapshot, error) {
	sn := Snapshot{TakenAt: time.Now().UTC(), AsOf: asOf.UTC(), VoteWeight: weight, ShareBasis: "arsa_payi", Owners: []SnapOwner{}, Unrepresented: []string{}}
	type unitRow struct {
		id     uuid.UUID
		door   string
		parent *uuid.UUID
	}
	rows, err := tx.Query(ctx, `SELECT id, door_number, parent_unit_id FROM unit
		WHERE org_id = $1 AND site_id = $2 AND status = 'active' AND type <> 'common' ORDER BY door_number, id`, orgID, siteID)
	if err != nil {
		return sn, err
	}
	var units []unitRow
	for rows.Next() {
		var u unitRow
		if err := rows.Scan(&u.id, &u.door, &u.parent); err != nil {
			rows.Close()
			return sn, err
		}
		units = append(units, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sn, err
	}
	shareOf := map[uuid.UUID]float64{}
	srows, err := tx.Query(ctx, `SELECT v.unit_id, v.value::float8 FROM allocation_key_value v
		JOIN allocation_key k ON k.org_id = v.org_id AND k.id = v.key_id
		WHERE v.org_id = $1 AND v.site_id = $2 AND k.code = 'arsa_payi'`, orgID, siteID)
	if err != nil {
		return sn, err
	}
	for srows.Next() {
		var id uuid.UUID
		var v float64
		if err := srows.Scan(&id, &v); err != nil {
			srows.Close()
			return sn, err
		}
		shareOf[id] = v
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return sn, err
	}

	// A unit with a parent (a parking spot or storage under a flat) votes with
	// the parent; its share is added to the parent's.
	inSet := map[uuid.UUID]bool{}
	for _, u := range units {
		inSet[u.id] = true
	}
	root := map[uuid.UUID]uuid.UUID{}
	roots := []unitRow{}
	for _, u := range units {
		if u.parent != nil && inSet[*u.parent] {
			root[u.id] = *u.parent
			continue
		}
		root[u.id] = u.id
		roots = append(roots, u)
	}
	grouped := map[uuid.UUID]float64{}
	total := 0.0
	for _, u := range units {
		grouped[root[u.id]] += shareOf[u.id]
		total += shareOf[u.id]
	}
	if total <= 0 {
		sn.ShareBasis = "equal"
		for _, r := range roots {
			grouped[r.id] = 1
		}
	}

	orows, err := tx.Query(ctx, `SELECT o.unit_id, o.person_id, p.display_name, o.is_correspondent, COALESCE(o.share_pct, 0)::float8, o.valid_from, o.id
		FROM occupancy o JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
		WHERE o.org_id = $1 AND o.site_id = $2 AND o.kind = 'owner' AND o.valid_from <= $3::date AND (o.valid_to IS NULL OR o.valid_to > $3::date)`,
		orgID, siteID, asOf)
	if err != nil {
		return sn, err
	}
	byUnit := map[uuid.UUID][]occRow{}
	for orows.Next() {
		var r occRow
		if err := orows.Scan(&r.unit, &r.person, &r.name, &r.correspond, &r.sharePct, &r.from, &r.id); err != nil {
			orows.Close()
			return sn, err
		}
		byUnit[r.unit] = append(byUnit[r.unit], r)
	}
	orows.Close()
	if err := orows.Err(); err != nil {
		return sn, err
	}

	owners := map[uuid.UUID]*SnapOwner{}
	ownedBy := map[uuid.UUID][]uuid.UUID{}
	doorOf := map[uuid.UUID]string{}
	var order []uuid.UUID
	for _, r := range roots {
		os := byUnit[r.id]
		if len(os) == 0 {
			sn.Unrepresented = append(sn.Unrepresented, r.door)
			continue
		}
		slices.SortFunc(os, func(a, b occRow) int {
			switch {
			case a.correspond != b.correspond:
				if a.correspond {
					return -1
				}
				return 1
			case a.sharePct != b.sharePct:
				if a.sharePct > b.sharePct {
					return -1
				}
				return 1
			case !a.from.Equal(b.from):
				if a.from.Before(b.from) {
					return -1
				}
				return 1
			}
			return slices.Compare(a.id[:], b.id[:])
		})
		for _, oc := range os {
			if !slices.Contains(ownedBy[oc.person], r.id) {
				ownedBy[oc.person] = append(ownedBy[oc.person], r.id)
				doorOf[r.id] = r.door
			}
		}
		rep := os[0]
		o := owners[rep.person]
		if o == nil {
			o = &SnapOwner{PersonID: rep.person, DisplayName: rep.name, Units: []SnapUnit{}}
			owners[rep.person] = o
			order = append(order, rep.person)
		}
		share := grouped[r.id]
		o.Units = append(o.Units, SnapUnit{UnitID: r.id, DoorNumber: r.door, Share: share})
		o.Share += share
		sn.UnitCount++
		sn.TotalShare += share
	}
	for _, p := range order {
		o := owners[p]
		if weight == VoteWeightPerOwner {
			o.Votes = 1
		} else {
			o.Votes = int64(len(o.Units))
		}
		o.VoteWeight = o.Votes
		o.ShareText = fmtShare(o.Share)
		o.UnitIDs, o.UnitLabels, o.RepresentativeOfUnitIDs = []uuid.UUID{}, []string{}, []uuid.UUID{}
		for _, un := range o.Units {
			o.RepresentativeOfUnitIDs = append(o.RepresentativeOfUnitIDs, un.UnitID)
		}
		for _, uid := range ownedBy[p] {
			o.UnitIDs = append(o.UnitIDs, uid)
			o.UnitLabels = append(o.UnitLabels, doorOf[uid])
		}
		sn.TotalVotes += o.Votes
		sn.Owners = append(sn.Owners, *o)
	}
	return sn, nil
}

// normalizeSnapshot fills the derived fields of a stored snapshot that an older
// writer left out, so the API always shows the typed shape.
func normalizeSnapshot(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var sn Snapshot
	if json.Unmarshal(raw, &sn) != nil {
		return raw
	}
	if sn.Owners == nil {
		sn.Owners = []SnapOwner{}
	}
	if sn.Unrepresented == nil {
		sn.Unrepresented = []string{}
	}
	for i := range sn.Owners {
		o := &sn.Owners[i]
		if o.Units == nil {
			o.Units = []SnapUnit{}
		}
		if o.VoteWeight == 0 {
			o.VoteWeight = o.Votes
		}
		if o.ShareText == "" {
			o.ShareText = fmtShare(o.Share)
		}
		if o.RepresentativeOfUnitIDs == nil {
			o.RepresentativeOfUnitIDs = []uuid.UUID{}
			for _, u := range o.Units {
				o.RepresentativeOfUnitIDs = append(o.RepresentativeOfUnitIDs, u.UnitID)
			}
		}
		if o.UnitIDs == nil {
			o.UnitIDs, o.UnitLabels = []uuid.UUID{}, []string{}
			for _, u := range o.Units {
				o.UnitIDs, o.UnitLabels = append(o.UnitIDs, u.UnitID), append(o.UnitLabels, u.DoorNumber)
			}
		}
		if o.UnitLabels == nil {
			o.UnitLabels = []string{}
		}
	}
	out, err := json.Marshal(sn)
	if err != nil {
		return raw
	}
	return out
}

// loadSnapshot reads the snapshot stored on a called meeting.
func loadSnapshot(raw []byte) (Snapshot, error) {
	var sn Snapshot
	if len(raw) == 0 {
		return sn, fmt.Errorf("meeting has no eligibility snapshot")
	}
	return sn, json.Unmarshal(raw, &sn)
}
