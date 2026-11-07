package access

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/access/session"
	"github.com/openbekci/openbekci/server/internal/core/audit"
)

// backend implements session.Backend on the service's tables.
type backend struct{ s *Service }

var _ session.Backend = (*backend)(nil)

// correctOccurred applies the clock offset the agent reported to an event time
// (design 4.9): corrected = reported + offset when |offset| <= 24 h, never later
// than the moment the server received it. The unadjusted value is returned
// when it differs from the stored one by more than 5 seconds.
func correctOccurred(reported time.Time, offsetMS *int64, synced time.Time) (occurred time.Time, rawReported *time.Time) {
	occurred = reported
	if offsetMS != nil {
		off := time.Duration(*offsetMS) * time.Millisecond
		if off >= -ClockBlockOffset && off <= ClockBlockOffset {
			occurred = reported.Add(off)
		}
	}
	if occurred.After(synced) {
		occurred = synced
	}
	if d := occurred.Sub(reported); d > 5*time.Second || d < -5*time.Second {
		r := reported
		rawReported = &r
	}
	return occurred, rawReported
}

func clockSuspect(reported bool, offsetMS *int64) bool {
	if reported {
		return true
	}
	if offsetMS == nil {
		return false
	}
	return math.Abs(float64(*offsetMS)) > float64(ClockSuspectOffset/time.Millisecond)
}

// driverJSON stores the drivers of a hello or heartbeat.
func driverJSON(d []protocol.DriverState) []byte {
	if d == nil {
		d = []protocol.DriverState{}
	}
	b, _ := json.Marshal(d)
	return b
}

func (b *backend) Hello(ctx context.Context, ref session.Ref, h protocol.AgentHello, now time.Time) (session.HelloInfo, error) {
	var info session.HelloInfo
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		var rotateBy *time.Time
		var rotateNow bool
		var prevOffset *int64
		if err := tx.QueryRow(ctx, `SELECT rotate_by, rotate_now, clock_offset_ms FROM agent WHERE org_id = $1 AND id = $2 AND status = 'active' FOR UPDATE`,
			ref.OrgID, ref.AgentID).Scan(&rotateBy, &rotateNow, &prevOffset); err != nil {
			return err
		}
		off := h.ClockOffsetMS
		if off == nil {
			off = prevOffset
		}
		if _, err := tx.Exec(ctx, `UPDATE agent SET agent_version = $3, platform = COALESCE(NULLIF($4, ''), platform), protocol_version = $5,
				last_seen = $6, snapshot_version = $7, config_version = $8, queue_depth = $9, drivers_status = $10,
				clock_offset_ms = $11, clock_suspect = $12, updated_at = now()
			WHERE org_id = $1 AND id = $2`, ref.OrgID, ref.AgentID, h.AgentVersion, h.Platform, protocol.Version, now,
			h.SnapshotVersion, h.ConfigVersion, h.QueueDepth, driverJSON(h.Drivers), off, clockSuspect(false, off)); err != nil {
			return err
		}
		cfg, err := loadSiteConfig(ctx, tx, ref.OrgID, ref.SiteID)
		if err != nil {
			return err
		}
		cv, err := currentConfigVersion(ctx, tx, ref.OrgID, ref.SiteID, cfg)
		if err != nil {
			return err
		}
		sv, err := siteVersion(ctx, tx, ref.OrgID, ref.SiteID)
		if err != nil {
			return err
		}
		info = session.HelloInfo{
			Site:            protocol.Site{ID: ref.SiteID.String(), Timezone: cfg.Timezone},
			SnapshotVersion: sv, ConfigVersion: cv, RotateBy: rotateBy, RotateNow: rotateNow, HeartbeatSeconds: HeartbeatSeconds,
		}
		return nil
	})
	return info, err
}

func (b *backend) Config(ctx context.Context, ref session.Ref) (session.ConfigInfo, error) {
	var out session.ConfigInfo
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		cfg, err := loadSiteConfig(ctx, tx, ref.OrgID, ref.SiteID)
		if err != nil {
			return err
		}
		v, err := currentConfigVersion(ctx, tx, ref.OrgID, ref.SiteID, cfg)
		if err != nil {
			return err
		}
		out = session.ConfigInfo{Version: v, Command: cfg.updateConfigFor(v, ref.SiteID, ref.AgentID)}
		return nil
	})
	return out, err
}

func (b *backend) SiteVersion(ctx context.Context, ref session.Ref) (int64, error) {
	var v int64
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) (err error) {
		v, err = siteVersion(ctx, tx, ref.OrgID, ref.SiteID)
		return err
	})
	return v, err
}

func (b *backend) Snapshot(ctx context.Context, ref session.Ref) (session.SnapshotData, error) {
	var out session.SnapshotData
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		v, entries, err := loadSnapshot(ctx, tx, ref.OrgID, ref.SiteID)
		if err != nil {
			return err
		}
		var tz string
		if err := tx.QueryRow(ctx, `SELECT timezone FROM site WHERE org_id = $1 AND id = $2`, ref.OrgID, ref.SiteID).Scan(&tz); err != nil {
			return err
		}
		out = session.SnapshotData{Version: v, Timezone: tz, Entries: entries}
		return nil
	})
	return out, err
}

func (b *backend) Deltas(ctx context.Context, ref session.Ref, from int64) ([]protocol.Delta, bool, error) {
	var out []protocol.Delta
	var ok bool
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) (err error) {
		out, ok, err = loadDeltas(ctx, tx, ref.OrgID, ref.SiteID, from)
		return err
	})
	return out, ok, err
}

func (b *backend) Touch(ctx context.Context, ref session.Ref, now time.Time) error {
	return b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE agent SET last_seen = $3 WHERE org_id = $1 AND id = $2`, ref.OrgID, ref.AgentID, now)
		return err
	})
}

func (b *backend) Heartbeat(ctx context.Context, ref session.Ref, h protocol.Heartbeat, now time.Time) error {
	return b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		var prev *int64
		if err := tx.QueryRow(ctx, `SELECT clock_offset_ms FROM agent WHERE org_id = $1 AND id = $2 FOR UPDATE`, ref.OrgID, ref.AgentID).Scan(&prev); err != nil {
			return err
		}
		off := h.ClockOffsetMS
		if off == nil {
			off = prev
		}
		var storeErr *string
		if h.StoreError != "" {
			storeErr = &h.StoreError
		}
		_, err := tx.Exec(ctx, `UPDATE agent SET last_seen = $3, snapshot_version = $4, queue_depth = $5, drivers_status = $6,
				clock_offset_ms = $7, clock_suspect = $8, lost_events = $9, store_error = $10, dead_letters = $11, updated_at = now()
			WHERE org_id = $1 AND id = $2`, ref.OrgID, ref.AgentID, now, h.SnapshotVersion, h.QueueDepth, driverJSON(h.Drivers),
			off, clockSuspect(h.ClockSuspect, off), max(h.LostEvents, h.DroppedEvents), storeErr, h.DeadLetters)
		return err
	})
}

// Events ingests the valid events of a batch (design 4.6): inserted with
// ON CONFLICT DO NOTHING (a repeat is a duplicate), points that are not this
// agent's are rejected as unknown_point, and the server stamps the actor from
// the command, never from what the agent claims.
func (b *backend) Events(ctx context.Context, ref session.Ref, offsetMS *int64, events []protocol.Event, now time.Time) (protocol.EventAckDetail, error) {
	detail := protocol.EventAckDetail{Accepted: []string{}, Duplicates: []string{}, Rejected: []protocol.Rejection{}}
	changed := false
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		detail = protocol.EventAckDetail{Accepted: []string{}, Duplicates: []string{}, Rejected: []protocol.Rejection{}}
		changed = false
		points, err := agentPointSet(ctx, tx, ref)
		if err != nil {
			return err
		}
		creds, err := knownCredentials(ctx, tx, ref, events)
		if err != nil {
			return err
		}
		cmds, err := knownCommands(ctx, tx, ref, events)
		if err != nil {
			return err
		}
		allowed := map[uuid.UUID]int{}
		for _, e := range events {
			pid, _ := uuid.Parse(e.PointID)
			if !points[pid] {
				detail.Rejected = append(detail.Rejected, protocol.Rejection{ID: e.ID, Code: protocol.RejectUnknownPoint})
				continue
			}
			eid := uuid.MustParse(e.ID)
			var credID *uuid.UUID
			if c, err := uuid.Parse(e.CredentialID); err == nil && creds[c] {
				credID = &c
			}
			var actor *uuid.UUID
			var actorName *string
			var cmdID, visitorID *uuid.UUID
			if c, err := uuid.Parse(e.CommandID); err == nil {
				if cmd, ok := cmds[c]; ok {
					cmdID, actor, actorName, visitorID = &c, cmd.actor, cmd.display, cmd.visitorEntry
					if cmd.credential != nil && credID == nil && creds[*cmd.credential] {
						credID = cmd.credential
					}
				}
			}
			occurred, rawReported := correctOccurred(e.OccurredAt.Time, offsetMS, now)
			payload, err := eventPayload(e.Detail, rawReported)
			if err != nil {
				detail.Rejected = append(detail.Rejected, protocol.Rejection{ID: e.ID, Code: protocol.RejectInvalid})
				continue
			}
			plate := NormalizePlate(e.ObservedPlate)
			ct, err := tx.Exec(ctx, `INSERT INTO access_event (id, org_id, site_id, point_id, agent_id, credential_id, decision, reason, presented_kind,
					observed_plate, actor_user_id, actor_display, command_id, visitor_entry_id, occurred_at, synced_at, raw_ref, raw_payload)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), $11, $12, $13, $14, $15, $16, NULLIF($17, ''), $18)
				ON CONFLICT (org_id, id) DO NOTHING`,
				eid, ref.OrgID, ref.SiteID, pid, ref.AgentID, credID, e.Decision, e.Reason, e.PresentedKind,
				plate, actor, actorName, cmdID, visitorID, occurred, now, e.RawRef, payload)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				detail.Duplicates = append(detail.Duplicates, e.ID)
				continue
			}
			detail.Accepted = append(detail.Accepted, e.ID)
			if e.Decision == protocol.DecisionAllow && credID != nil {
				allowed[*credID]++
			}
		}
		ch, err := countUses(ctx, tx, ref, allowed)
		changed = ch
		return err
	})
	if err != nil {
		return protocol.EventAckDetail{}, err
	}
	if changed {
		b.s.WakeSite(ref.SiteID)
	}
	return detail, nil
}

func eventPayload(detail json.RawMessage, rawReported *time.Time) ([]byte, error) {
	if len(detail) == 0 && rawReported == nil {
		return nil, nil
	}
	m := map[string]any{}
	if len(detail) > 0 {
		if err := json.Unmarshal(detail, &m); err != nil {
			return nil, err
		}
	}
	if rawReported != nil {
		m["reported_at"] = protocol.At(*rawReported)
	}
	return json.Marshal(m)
}

func agentPointSet(ctx context.Context, tx pgx.Tx, ref session.Ref) (map[uuid.UUID]bool, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM access_point WHERE org_id = $1 AND site_id = $2 AND agent_id = $3`, ref.OrgID, ref.SiteID, ref.AgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func knownCredentials(ctx context.Context, tx pgx.Tx, ref session.Ref, events []protocol.Event) (map[uuid.UUID]bool, error) {
	var ids []uuid.UUID
	for _, e := range events {
		if id, err := uuid.Parse(e.CredentialID); err == nil {
			ids = append(ids, id)
		}
	}
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	return out, scanIDs(ctx, tx, out, `SELECT id FROM credential WHERE org_id = $1 AND site_id = $2 AND id = ANY($3)`, ref.OrgID, ref.SiteID, ids)
}

func scanIDs(ctx context.Context, tx pgx.Tx, into map[uuid.UUID]bool, q string, args ...any) error {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		into[id] = true
	}
	return rows.Err()
}

type cmdInfo struct {
	actor        *uuid.UUID
	display      *string
	visitorEntry *uuid.UUID
	credential   *uuid.UUID
}

func knownCommands(ctx context.Context, tx pgx.Tx, ref session.Ref, events []protocol.Event) (map[uuid.UUID]cmdInfo, error) {
	var ids []uuid.UUID
	for _, e := range events {
		if id, err := uuid.Parse(e.CommandID); err == nil {
			ids = append(ids, id)
		}
	}
	out := map[uuid.UUID]cmdInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id, actor_user_id, actor_display, payload FROM agent_command
		WHERE org_id = $1 AND site_id = $2 AND agent_id = $3 AND id = ANY($4)`, ref.OrgID, ref.SiteID, ref.AgentID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var ci cmdInfo
		var payload []byte
		if err := rows.Scan(&id, &ci.actor, &ci.display, &payload); err != nil {
			return nil, err
		}
		var c protocol.Command
		if json.Unmarshal(payload, &c) == nil {
			if v, err := uuid.Parse(c.VisitorEntryID); err == nil {
				ci.visitorEntry = &v
			}
			if v, err := uuid.Parse(c.CredentialID); err == nil {
				ci.credential = &v
			}
		}
		out[id] = ci
	}
	return out, rows.Err()
}

// countUses adds the allowed events to the use count of their credentials;
// a credential that reached max_uses is revoked (used_up) and leaves the
// allow-list by delta (design 6.4). It reports whether a version was taken.
func countUses(ctx context.Context, tx pgx.Tx, ref session.Ref, allowed map[uuid.UUID]int) (bool, error) {
	changed := false
	var points []pointRow
	for credID, n := range allowed {
		var uses int
		var maxUses *int
		var status string
		if err := tx.QueryRow(ctx, `UPDATE credential SET uses = uses + $4, updated_at = now() WHERE org_id = $1 AND site_id = $2 AND id = $3
			RETURNING uses, max_uses, status`, ref.OrgID, ref.SiteID, credID, n).Scan(&uses, &maxUses, &status); err != nil {
			return changed, err
		}
		if maxUses == nil || uses < *maxUses || status != StatusActive {
			continue
		}
		if points == nil {
			var err error
			if points, err = loadPoints(ctx, tx, ref.OrgID, ref.SiteID); err != nil {
				return changed, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE credential SET status = 'revoked', status_reason = 'used_up', updated_at = now() WHERE org_id = $1 AND id = $2`, ref.OrgID, credID); err != nil {
			return changed, err
		}
		ok, err := syncCredentialByID(ctx, tx, ref.OrgID, ref.SiteID, credID, points)
		if err != nil {
			return changed, err
		}
		changed = changed || ok
	}
	return changed, nil
}

// PendingCommands marks overdue commands expired and delivers the rest.
func (b *backend) PendingCommands(ctx context.Context, ref session.Ref, now time.Time) ([]session.OutCommand, error) {
	var out []session.OutCommand
	err := b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		out = nil
		if _, err := tx.Exec(ctx, `UPDATE agent_command SET status = 'expired', completed_at = $4, updated_at = now(),
				result = COALESCE(result, '{"status":"expired","reason":"not_delivered"}')
			WHERE org_id = $1 AND site_id = $2 AND agent_id = $3 AND status = 'pending' AND expires_at <= $4`, ref.OrgID, ref.SiteID, ref.AgentID, now); err != nil {
			return err
		}
		// delivered but never answered, well past the expiry: the outcome stays unknown, it is not retried
		if _, err := tx.Exec(ctx, `UPDATE agent_command SET status = 'expired', completed_at = $4, updated_at = now(),
				result = COALESCE(result, '{"status":"expired","reason":"no_answer"}')
			WHERE org_id = $1 AND site_id = $2 AND agent_id = $3 AND status = 'delivered' AND expires_at <= $4::timestamptz - interval '30 seconds'`,
			ref.OrgID, ref.SiteID, ref.AgentID, now); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE agent_command SET status = 'delivered', delivered_at = $4, updated_at = now()
			WHERE org_id = $1 AND site_id = $2 AND agent_id = $3 AND status = 'pending' AND expires_at > $4
			RETURNING id, payload, issued_at`, ref.OrgID, ref.SiteID, ref.AgentID, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		type row struct {
			oc session.OutCommand
			at time.Time
		}
		var got []row
		for rows.Next() {
			var id uuid.UUID
			var payload []byte
			var at time.Time
			if err := rows.Scan(&id, &payload, &at); err != nil {
				return err
			}
			var c protocol.Command
			if err := json.Unmarshal(payload, &c); err != nil {
				return fmt.Errorf("command %s: %w", id, err)
			}
			got = append(got, row{session.OutCommand{ID: id, Command: c}, at})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := 1; i < len(got); i++ { // oldest first
			for j := i; j > 0 && got[j].at.Before(got[j-1].at); j-- {
				got[j], got[j-1] = got[j-1], got[j]
			}
		}
		for _, g := range got {
			out = append(out, g.oc)
		}
		return nil
	})
	return out, err
}

// CommandAck records the agent's answer. The first answer wins; the agent
// re-acks a replayed command with its stored result, which changes nothing.
func (b *backend) CommandAck(ctx context.Context, ref session.Ref, id uuid.UUID, a protocol.Ack, now time.Time) error {
	status := "failed"
	switch a.Status {
	case protocol.AckOK, protocol.AckDone:
		status = "done"
	case protocol.AckExpired:
		status = "expired"
	}
	var detail map[string]any
	if len(a.Detail) > 0 {
		_ = json.Unmarshal(a.Detail, &detail)
	}
	reason, _ := detail["reason"].(string)
	result, _ := json.Marshal(map[string]any{"status": a.Status, "reason": reason, "detail": detail})
	return b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		var cmd string
		var pointID *uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE agent_command SET status = $4, result = $5, completed_at = $6, updated_at = now()
			WHERE org_id = $1 AND site_id = $2 AND agent_id = $3 AND id = $7 AND status IN ('pending', 'delivered')
			RETURNING command, point_id`, ref.OrgID, ref.SiteID, ref.AgentID, status, result, now, id).Scan(&cmd, &pointID)
		if err == pgx.ErrNoRows {
			return nil // not ours, unknown, or already answered
		}
		if err != nil {
			return err
		}
		if cmd != protocol.CmdOpenPoint || pointID == nil {
			return nil
		}
		diff, err := audit.Diff("agent_command", nil, map[string]any{"status": status, "reason": reason, "ack": a.Status})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: ref.OrgID, SiteID: &ref.SiteID, ActorKind: audit.ActorSystem,
			Action: "access.open_completed", TargetType: "agent_command", TargetID: id, Diff: diff})
	})
}

func (b *backend) ConfigApplied(ctx context.Context, ref session.Ref, v int64) error {
	return b.s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE agent SET config_version = $3 WHERE org_id = $1 AND id = $2`, ref.OrgID, ref.AgentID, v)
		return err
	})
}
