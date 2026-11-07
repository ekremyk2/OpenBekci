package community

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
)

// Job schedules.
const (
	// RequestExpiryInterval is how often open approval requests are expired.
	RequestExpiryInterval = time.Minute
	// RetentionInterval is how often the visitor logs are pruned.
	RetentionInterval = 6 * time.Hour
)

// RequestExpiryArgs is the arguments of community.visitor_request_expiry.
type RequestExpiryArgs struct{}

// Kind implements river.JobArgs.
func (RequestExpiryArgs) Kind() string { return "community.visitor_request_expiry" }

// RetentionArgs is the arguments of community.visitor_retention_prune.
type RetentionArgs struct{}

// Kind implements river.JobArgs.
func (RetentionArgs) Kind() string { return "community.visitor_retention_prune" }

type expiryWorker struct {
	river.WorkerDefaults[RequestExpiryArgs]
	s *Service
}

func (w *expiryWorker) Work(ctx context.Context, _ *river.Job[RequestExpiryArgs]) error {
	n, err := w.s.ExpireVisitors(ctx)
	if err != nil {
		return fmt.Errorf("expire visitors: %w", err)
	}
	if n > 0 {
		w.s.log.InfoContext(ctx, "expired visitor requests and entries", "count", n)
	}
	return nil
}

type retentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	s *Service
}

func (w *retentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	n, err := w.s.PruneVisitorLogs(ctx)
	if err != nil {
		return fmt.Errorf("prune visitor logs: %w", err)
	}
	if n > 0 {
		w.s.log.InfoContext(ctx, "pruned visitor logs", "rows", n)
	}
	return nil
}

// RegisterWorkers adds the module's River workers (jobs.Options.Register).
func (s *Service) RegisterWorkers(w *river.Workers) {
	river.AddWorker(w, &expiryWorker{s: s})
	river.AddWorker(w, &retentionWorker{s: s})
}

// PeriodicJobs are the module's schedules (jobs.Options.Periodic).
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(RequestExpiryInterval),
			func() (river.JobArgs, *river.InsertOpts) { return RequestExpiryArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(RetentionInterval),
			func() (river.JobArgs, *river.InsertOpts) { return RetentionArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

// orgIDs lists the active organizations (the jobs run one tenant transaction each).
func (s *Service) orgIDs(ctx context.Context) ([]uuid.UUID, error) {
	if s.cfg.Platform == nil {
		return nil, nil
	}
	var out []uuid.UUID
	err := s.cfg.Platform.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM organization WHERE status = 'active' ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}

// ExpireVisitors expires the approval requests nobody answered in time and
// the visitor entries whose window (plus slack) is over without arrival, in
// every organization. It returns how many rows changed.
func (s *Service) ExpireVisitors(ctx context.Context) (int, error) {
	orgs, err := s.orgIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, org := range orgs {
		n, err := s.ExpireVisitorsOrg(ctx, org)
		if err != nil {
			return total, fmt.Errorf("organization %s: %w", org, err)
		}
		total += n
	}
	return total, nil
}

// ExpireVisitorsOrg is ExpireVisitors for one organization.
func (s *Service) ExpireVisitorsOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	now := s.now()
	total := 0
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE visitor_approval_request SET status = 'expired', updated_at = now()
			WHERE org_id = $1 AND status = 'pending' AND expires_at <= $2 RETURNING site_id, entry_id`, orgID, now)
		if err != nil {
			return err
		}
		type pair struct{ site, entry uuid.UUID }
		var expired []pair
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.site, &p.entry); err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range expired {
			if _, err := tx.Exec(ctx, `UPDATE visitor_entry SET approval_status = 'expired', updated_at = now(), version = version + 1
				WHERE org_id = $1 AND id = $2 AND approval_status = 'requested'`, orgID, p.entry); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &p.site, ActorKind: audit.ActorJob, Action: "visitor.request_expired", TargetType: "visitor_entry", TargetID: p.entry}); err != nil {
				return err
			}
			if err := s.visitorChanged(ctx, tx, p.site, "visitor_entry", p.entry); err != nil {
				return err
			}
		}
		total += len(expired)
		// single-entry visitors that never came: bookkeeping, the gate evaluates the window itself
		tag, err := tx.Exec(ctx, `UPDATE visitor_entry SET approval_status = 'expired', updated_at = now(), version = version + 1
			WHERE org_id = $1 AND approval_status IN ('pre_approved', 'approved') AND arrived_at IS NULL AND NOT cancelled
			  AND recurring_visitor_id IS NULL AND window_to + interval '30 minutes' < $2`, orgID, now)
		if err != nil {
			return err
		}
		total += int(tag.RowsAffected())
		return nil
	})
	return total, err
}

// PruneVisitorLogs deletes visitor logs older than each site's retention
// (visitor_retention_days, default 90; KVKK) in every organization: visitor
// entries and their requests, recurring visitors that ended, collected
// parcels, and the PIN attempt and guard session records. It returns the rows
// deleted.
func (s *Service) PruneVisitorLogs(ctx context.Context) (int, error) {
	orgs, err := s.orgIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, org := range orgs {
		n, err := s.PruneVisitorLogsOrg(ctx, org)
		if err != nil {
			return total, fmt.Errorf("organization %s: %w", org, err)
		}
		total += n
	}
	return total, nil
}

// PruneVisitorLogsOrg is PruneVisitorLogs for one organization.
func (s *Service) PruneVisitorLogsOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	now := s.now()
	total := 0
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, settings FROM site WHERE org_id = $1`, orgID)
		if err != nil {
			return err
		}
		type site struct {
			id   uuid.UUID
			days int
		}
		var sites []site
		for rows.Next() {
			var id uuid.UUID
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			si := siteInfo{Settings: map[string]any{}}
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &si.Settings)
			}
			sites = append(sites, site{id, si.retentionDays()})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, st := range sites {
			cutoff := now.AddDate(0, 0, -st.days)
			for _, q := range []string{
				// requests go with their entry (ON DELETE CASCADE); a visit of a recurring visitor is an entry too
				`DELETE FROM visitor_entry WHERE org_id = $1 AND site_id = $2 AND window_to < $3`,
				`DELETE FROM delivery WHERE org_id = $1 AND site_id = $2 AND collected_at IS NOT NULL AND collected_at < $3`,
				`DELETE FROM recurring_visitor r WHERE r.org_id = $1 AND r.site_id = $2 AND r.updated_at < $3::timestamptz
				   AND (r.status = 'revoked' OR r.valid_to < ($3::timestamptz)::date) AND NOT EXISTS (SELECT 1 FROM visitor_entry e WHERE e.org_id = r.org_id AND e.recurring_visitor_id = r.id)`,
				`DELETE FROM gate_pin_attempt WHERE org_id = $1 AND site_id = $2 AND at < $3`,
				`DELETE FROM guard_session WHERE org_id = $1 AND site_id = $2 AND expires_at < $3`,
			} {
				tag, err := tx.Exec(ctx, q, orgID, st.id, cutoff)
				if err != nil {
					return err
				}
				total += int(tag.RowsAffected())
			}
		}
		return nil
	})
	return total, err
}
