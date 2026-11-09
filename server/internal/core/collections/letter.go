package collections

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"text/template"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/packdocs"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
	"github.com/openbekci/openbekci/server/internal/platform/pdf"
	"github.com/openbekci/openbekci/server/internal/problem"
	"github.com/openbekci/openbekci/server/locales"
)

// LetterDays is the payment deadline the ihtarname sets, in days.
const LetterDays = 7

type letterData struct {
	SiteName, Date, DebtorName, Door, SideLabel, AsOf, Total, Principal, LateFee string
	Days                                                                         int
}

// occupantName returns the person of a unit side on a civil date.
func occupantName(ctx context.Context, tx pgx.Tx, orgID, unitID uuid.UUID, side, on string) (*uuid.UUID, string, error) {
	var pid uuid.UUID
	var name string
	err := tx.QueryRow(ctx, `SELECT p.id, p.display_name FROM occupancy o JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
		WHERE o.org_id=$1 AND o.unit_id=$2 AND o.kind=$3 AND o.valid_from <= $4::date AND (o.valid_to IS NULL OR o.valid_to > $4::date)
		ORDER BY o.is_correspondent DESC, o.valid_from LIMIT 1`, orgID, unitID, side, on).Scan(&pid, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	return &pid, name, err
}

// letterTx renders the ihtarname of a debtor as a PDF inside a transaction (the caller has checked permissions).
func (s *Service) letterTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, d billing.Debtor, asOf string) ([]byte, error) {
	b, err := s.breakdownTx(ctx, tx, orgID, site, d, asOf)
	if err != nil {
		return nil, err
	}
	data := letterData{SiteName: site.Name, Date: s.Now().In(ledger.Location(site.Timezone)).Format("02.01.2006"), AsOf: asOfTR(asOf), Days: LetterDays,
		Total: FormatTL(b.TotalPrincipal.Amount + b.TotalLateFee.Amount), Principal: FormatTL(b.TotalPrincipal.Amount), LateFee: FormatTL(b.TotalLateFee.Amount)}
	switch {
	case d.IsPerson():
		data.SideLabel = "devralınan borç sahibi"
		data.Door = "-"
		if err := tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id=$1 AND id=$2`, orgID, *d.PersonID).Scan(&data.DebtorName); err != nil {
			return nil, err
		}
		var door string
		if err := tx.QueryRow(ctx, `SELECT u.door_number FROM charge c JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id WHERE c.org_id=$1 AND c.debtor_kind='person' AND c.debtor_person_id=$2 ORDER BY c.due_date DESC LIMIT 1`, orgID, *d.PersonID).Scan(&door); err == nil {
			data.Door = door
		}
	default:
		if err := tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id=$1 AND id=$2`, orgID, *d.UnitID).Scan(&data.Door); err != nil {
			return nil, err
		}
		data.SideLabel = map[string]string{"owner": "kat maliki", "tenant": "kiracı"}[d.Kind]
		_, name, err := occupantName(ctx, tx, orgID, *d.UnitID, d.Kind, asOf)
		if err != nil {
			return nil, err
		}
		data.DebtorName = name
		if name == "" {
			data.DebtorName = map[string]string{"owner": "Kat Maliki", "tenant": "Kiracı"}[d.Kind]
		}
	}
	// a pack's template (the Turkish ihtarname citing Law 634) replaces the generic letter
	breakdown := make([]any, 0, len(b.Lines))
	for _, l := range b.Lines {
		p := ""
		if l.Period != nil {
			p = *l.Period
		}
		breakdown = append(breakdown, map[string]any{"period": p, "due": l.DueDate, "principal": FormatTL(l.Principal.Amount), "late_fee": FormatTL(l.LateFee.Amount)})
	}
	if out, ok := packdocs.Render(ctx, s.PackDocs, nil, orgID, doctpl.KindDemandLetter, "tr", map[string]any{
		"site_name": data.SiteName, "date": data.Date, "debtor_name": data.DebtorName, "door": data.Door, "side_label": data.SideLabel, "as_of": data.AsOf,
		"total": data.Total, "principal": data.Principal, "late_fee": data.LateFee, "days": data.Days, "breakdown": breakdown}); ok {
		return out, nil
	}
	text, err := locales.Template("tr", "ihtarname")
	if err != nil {
		return nil, problem.Internal(err)
	}
	t, err := template.New("ihtarname").Parse(text)
	if err != nil {
		return nil, problem.Internal(err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, problem.Internal(err)
	}
	doc, err := pdf.NewUnicode("İhtarname")
	if err != nil {
		return nil, problem.Internal(err)
	}
	paras := strings.Split(strings.TrimSpace(buf.String()), "\n\n")
	for i, p := range paras {
		switch i {
		case 0:
			lines := strings.Split(p, "\n")
			doc.Line(lines[0], 13, true)
			doc.Advance(8)
			doc.Line(lines[len(lines)-1], 20, true)
		default:
			doc.Paragraph(p, 11, false)
		}
		doc.Advance(8)
		if i == 3 {
			b.drawTable(doc, "Borç dökümü")
			doc.Advance(10)
		}
	}
	return doc.Bytes(), nil
}

func asOfTR(day string) string {
	if len(day) == 10 {
		return day[8:10] + "." + day[5:7] + "." + day[:4]
	}
	return day
}

// LetterInput selects what to generate: the letter of an event, or one for a debtor.
type LetterInput struct {
	EventID *uuid.UUID
	SiteID  uuid.UUID
	Debtor  *billing.Debtor
	AsOf    string
}

// Letter returns the ihtarname PDF (collections.manage). For an event it is the
// letter stored on the event, generated and stored when the event has none
// (TODO: file it into documents when the files module exists); for a debtor it is
// generated on the spot, as of asOf (default today), and not stored.
func (s *Service) Letter(ctx context.Context, orgID uuid.UUID, in LetterInput) ([]byte, error) {
	var out []byte
	err := s.run(ctx, orgID, "dunning event", func(tx pgx.Tx) error {
		if in.EventID != nil {
			var site uuid.UUID
			var unit, person *uuid.UUID
			var kind *string
			var sent string
			var stored []byte
			if err := tx.QueryRow(ctx, `SELECT site_id, unit_id, debtor_kind, person_id, to_char(sent_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), letter FROM dunning_event WHERE org_id=$1 AND id=$2`, orgID, *in.EventID).
				Scan(&site, &unit, &kind, &person, &sent, &stored); err != nil {
				return err
			}
			if err := ledger.RequireSite(ctx, tx, ledger.PermCollectionsManage, site); err != nil {
				return err
			}
			if len(stored) > 0 {
				out = stored
				return nil
			}
			st, err := tenancy.LoadSite(ctx, tx, orgID, site, false, true)
			if err != nil {
				return err
			}
			d := debtorOf(unit, kind, person)
			if out, err = s.letterTx(ctx, tx, orgID, st, d, sent); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE dunning_event SET letter=$3, updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, *in.EventID, out)
			return err
		}
		if in.Debtor == nil {
			return bad("debtor", "required")
		}
		if err := in.Debtor.Validate(); err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermCollectionsManage, orgID, in.SiteID)
		if err != nil {
			return err
		}
		asOf := in.AsOf
		if asOf == "" {
			asOf = ledger.Today(site.Timezone, s.Now())
		} else if _, err := ledger.ParseDate(asOf); err != nil {
			return bad("as_of", "invalid_format")
		}
		if err := s.checkDebtor(ctx, tx, orgID, in.SiteID, *in.Debtor); err != nil {
			return err
		}
		out, err = s.letterTx(ctx, tx, orgID, site, *in.Debtor, asOf)
		return err
	})
	return out, err
}

func debtorOf(unit *uuid.UUID, kind *string, person *uuid.UUID) billing.Debtor {
	if unit != nil && kind != nil {
		return billing.Debtor{UnitID: unit, Kind: *kind}
	}
	return billing.Debtor{PersonID: person}
}
