package governance

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// assemblyYearDue says for which year the ordinary assembly reminder is due
// today: the reminder window runs from the first day of the month before the
// assembly month to the last day of the assembly month. ok is false outside
// the window.
func assemblyYearDue(today time.Time, month int) (year int, ok bool) {
	for _, y := range []int{today.Year() - 1, today.Year(), today.Year() + 1} {
		start := time.Date(y, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		from, to := start.AddDate(0, -1, 0), start.AddDate(0, 1, -1)
		if !today.Before(from) && !today.After(to) {
			return y, true
		}
	}
	return 0, false
}

// assemblyRemindersOrg sends the yearly "ordinary assembly due" reminder (G6)
// to the managers of every active site of the organization, once per site and
// year, in the month before ordinary_assembly_month; a site that already has an
// assembly of that year (not cancelled) is not reminded.
func (s *Service) assemblyRemindersOrg(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	today := s.today()
	rows, err := tx.Query(ctx, `SELECT id FROM site WHERE org_id = $1 AND status = 'active' ORDER BY id`, orgID)
	if err != nil {
		return err
	}
	var sites []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		sites = append(sites, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, site := range sites {
		set, err := loadSettings(ctx, tx, orgID, site, false)
		if err != nil {
			return err
		}
		year, due := assemblyYearDue(today, int(set.Settings.OrdinaryAssemblyMonth))
		if !due {
			continue
		}
		var held bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meeting WHERE org_id = $1 AND site_id = $2 AND kind = 'general_assembly' AND status <> 'cancelled'
			AND EXTRACT(year FROM scheduled_at AT TIME ZONE 'UTC') = $3)`, orgID, site, year).Scan(&held); err != nil {
			return err
		}
		if held {
			continue
		}
		tag, err := tx.Exec(ctx, `INSERT INTO assembly_reminder (org_id, site_id, year) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, orgID, site, year)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		people, err := termRecipients(ctx, tx, orgID, site, uuid.Nil)
		if err != nil {
			return err
		}
		if len(people) > 0 && people[0] == uuid.Nil {
			people = people[1:]
		}
		if s.notifier != nil && len(people) > 0 {
			month := time.Month(set.Settings.OrdinaryAssemblyMonth).String()
			if err := s.notifier.Notify(ctx, tx, jobs.NotifyArgs{OrgID: orgID, SiteID: &site, Type: "meeting.assembly_due", Recipients: recipients(people),
				Channels: []string{"in_app", "email"}, Payload: map[string]any{"site_id": site, "year": year, "month": month},
				Link: "/sites/" + site.String() + "/meetings", Key: fmt.Sprintf("governance.ordinary_assembly:%s:%d", site, year)}); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("assembly_reminder", nil, map[string]any{"year": year, "month": set.Settings.OrdinaryAssemblyMonth})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &site, ActorKind: audit.ActorJob, Action: "assembly_reminder.create", TargetType: "site", TargetID: site, Diff: diff}); err != nil {
			return err
		}
	}
	return nil
}
