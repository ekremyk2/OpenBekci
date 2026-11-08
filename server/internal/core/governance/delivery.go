package governance

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// FileChecker tells whether a file exists in the organization (the files
// service, D1, implements it). The default knows no files, so a proof file can
// only be attached once the file store is wired in.
type FileChecker interface {
	FileExists(ctx context.Context, tx pgx.Tx, orgID, fileID uuid.UUID) (bool, error)
}

type noFiles struct{}

func (noFiles) FileExists(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

// SetFileChecker sets the file existence check (the files module registers here).
func (s *Service) SetFileChecker(f FileChecker) {
	if f != nil {
		s.files = f
	}
}

// pendingAbsentees are the absent owners whose decisions notice is not
// recorded yet: a delivery already recorded is never repeated.
func pendingAbsentees(ctx context.Context, tx pgx.Tx, m Meeting) ([]SnapOwner, error) {
	abs, _, err := absentees(ctx, tx, m)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT person_id FROM meeting_notice WHERE org_id = $1 AND meeting_id = $2 AND phase = 'decisions' AND status = 'recorded'`, m.OrgID, m.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	done := map[uuid.UUID]bool{}
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		done[p] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []SnapOwner
	for _, o := range abs {
		if !done[o.PersonID] {
			out = append(out, o)
		}
	}
	return out, nil
}

// notifyAbsentees is the manual :notify-decisions: it counts (dry_run) or
// schedules the delivery of the decisions to the absent owners. The delivery
// itself, and with it the legal record (a decisions notice per owner), happens
// when the job runs (deliverDecisions), not here; the manager can only send
// once decisions exist, and an owner whose delivery is recorded is skipped.
func (s *Service) notifyAbsentees(ctx context.Context, tx pgx.Tx, m Meeting, sc authz.Scope, channels []string, dryRun bool) (DecisionNotifyResult, error) {
	res := DecisionNotifyResult{DryRun: dryRun}
	if m.Status != StatusClosed {
		return res, problem.Conflict("the decisions are sent after the meeting is closed")
	}
	var decisions int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM decision WHERE org_id = $1 AND meeting_id = $2`, m.OrgID, m.ID).Scan(&decisions); err != nil {
		return res, err
	}
	if decisions == 0 {
		return res, problem.Conflict("the meeting has no decisions yet; record them first")
	}
	abs, err := pendingAbsentees(ctx, tx, m)
	if err != nil {
		return res, err
	}
	res.RecipientCount = int64(len(abs))
	if slices.Contains(channels, "sms") {
		res.SMSCount = res.RecipientCount
	}
	if dryRun || len(abs) == 0 {
		return res, nil
	}
	if s.notifier != nil {
		return res, s.notifier.EnqueueNotifyAbsentees(ctx, tx, m.OrgID, m.ID, channels)
	}
	return res, s.deliverDecisions(ctx, tx, m, sc, channels) // no queue: deliver in place
}

// deliverDecisions is the body of the delivery job: for each absent owner
// without a recorded delivery it sends the notification (deduplicated by Key)
// and records the decisions notice with the time of delivery. The notice is
// recorded here, at delivery, not when the job was scheduled. An electronic
// channel counts as the legal method only where the site allows it.
func (s *Service) deliverDecisions(ctx context.Context, tx pgx.Tx, m Meeting, sc authz.Scope, channels []string) error {
	abs, err := pendingAbsentees(ctx, tx, m)
	if err != nil {
		return err
	}
	if len(abs) == 0 {
		return nil
	}
	now := s.now().UTC()
	method := "other"
	if m.rules().NoticeElectronicAllowed {
		switch {
		case slices.Contains(channels, "email"):
			method = "email"
		case slices.Contains(channels, "sms"):
			method = "sms"
		}
	}
	si, err := loadSiteInfo(ctx, tx, m.OrgID, m.SiteID)
	if err != nil {
		return err
	}
	for _, o := range abs {
		if s.notifier != nil {
			if err := s.notifier.Notify(ctx, tx, jobs.NotifyArgs{OrgID: m.OrgID, SiteID: &m.SiteID, Type: "meeting.decisions", Recipients: recipients([]uuid.UUID{o.PersonID}), Channels: channels,
				Payload: map[string]any{"title": si.Name, "body": fmt.Sprintf("%s: %s", si.Name, fmtTime(m.ScheduledAt, si.Loc, si.Locale)), "meeting_id": m.ID},
				Link:    "/meetings/" + m.ID.String(), Key: "governance.decisions:" + m.ID.String() + ":" + o.PersonID.String()}); err != nil {
				return err
			}
		}
		note := "channels: " + fmt.Sprint(channels)
		if _, err := s.recordNotice(ctx, tx, m, sc, Snapshot{Owners: abs}, o.PersonID, NoticeRecord{Phase: "decisions", Method: method, SentAt: now, Note: &note}, false); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE decision SET notified_at = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND meeting_id = $2 AND notified_at IS NULL`, m.OrgID, m.ID, now); err != nil {
		return err
	}
	diff, err := audit.Diff("meeting", nil, map[string]any{"decisions_delivered": len(abs), "channels": channels})
	if err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{OrgID: m.OrgID, SiteID: sc.SiteID, Action: "meeting.deliver_decisions", TargetType: "meeting", TargetID: m.ID, Diff: diff})
}
