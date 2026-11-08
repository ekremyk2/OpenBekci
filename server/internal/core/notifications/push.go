package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/devices"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/push"
)

// DeviceSource is the part of the devices module the dispatcher needs
// (*devices.Service).
type DeviceSource interface {
	ForUsers(ctx context.Context, userIDs []uuid.UUID) ([]devices.Target, error)
	Disable(ctx context.Context, ids []uuid.UUID, reason string) error
}

// InstanceIDFunc returns the instance id carried in every push payload.
type InstanceIDFunc func(ctx context.Context) (uuid.UUID, error)

// pushEnabled reports whether the push channel is wired.
func (s *Service) pushEnabled() bool {
	return s.cfg.Push != nil && s.cfg.Devices != nil && s.cfg.InstanceID != nil
}

type pushResult struct {
	p    pendingSend
	err  error
	skip string
}

// surgeMinResults is the smallest batch the invalid_token pruning guard judges.
const surgeMinResults = 10

// maxCollapseKey is what APNs accepts as apns-collapse-id.
const maxCollapseKey = 64

// collapseKey is the event key, or a digest of it when too long for APNs.
func collapseKey(key string) string {
	if len(key) <= maxCollapseKey {
		return key
	}
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:16])
}

// deliverPush sends the content-free payload of each claimed push delivery to
// every device of its recipient. A delivery is sent when at least one device
// accepted it, skipped when no device could be reached for good (invalid
// tokens, no relay), and failed (retried by the job) when only retryable
// failures remain. Devices a relay reports invalid are disabled. It never
// returns an error: a failure is recorded on the deliveries, so the email and
// SMS results of the same job are still written.
func (s *Service) deliverPush(ctx context.Context, a jobs.NotifyArgs, sends []pendingSend, contacts map[uuid.UUID]contact) []pushResult {
	failAll := func(err error) []pushResult {
		out := make([]pushResult, len(sends))
		for i, p := range sends {
			out[i] = pushResult{p: p, err: err}
		}
		return out
	}
	if !s.pushEnabled() {
		out := make([]pushResult, len(sends))
		for i, p := range sends {
			out[i] = pushResult{p: p, skip: "push_unavailable"}
		}
		return out
	}
	instanceID, err := s.cfg.InstanceID(ctx)
	if err != nil {
		return failAll(fmt.Errorf("instance id: %w", err))
	}
	// badge: the person's unread in-app notifications in this organization
	badges := map[uuid.UUID]int{}
	err = s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		for _, p := range sends {
			if _, ok := badges[p.person]; ok {
				continue
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*)`+notifFrom+` WHERE n.org_id = $1 AND n.person_id = $2 AND n.read_at IS NULL`, a.OrgID, p.person).Scan(&n); err != nil {
				return err
			}
			badges[p.person] = n
		}
		return nil
	})
	if err != nil {
		return failAll(fmt.Errorf("unread count: %w", err))
	}
	var uids []uuid.UUID
	for _, p := range sends {
		if u := contacts[p.person].userID; u != nil {
			uids = append(uids, *u)
		}
	}
	targets, err := s.cfg.Devices.ForUsers(ctx, uids)
	if err != nil {
		return failAll(err)
	}
	byUser := map[uuid.UUID][]devices.Target{}
	for _, t := range targets {
		byUser[t.UserID] = append(byUser[t.UserID], t)
	}
	type owner struct {
		send   int
		device uuid.UUID
	}
	var msgs []push.Message
	var owners []owner
	for i, p := range sends {
		u := contacts[p.person].userID
		if u == nil {
			continue
		}
		for _, t := range byUser[*u] {
			msgs = append(msgs, push.Message{
				Kind: t.PushKind, Token: t.Token, RelayID: t.RelayID,
				Data: push.Payload{
					InstanceID: instanceID.String(), OrgID: a.OrgID.String(), NotificationID: p.notificationID.String(),
					Type: a.Type, CollapseKey: collapseKey(a.Key), Badge: min(badges[p.person], 99999),
				},
			})
			owners = append(owners, owner{send: i, device: t.ID})
		}
	}
	var results []push.Result
	if len(msgs) > 0 {
		results, err = s.cfg.Push.Send(ctx, msgs)
		if err != nil || len(results) != len(msgs) {
			if err == nil {
				err = errors.New("push sender returned the wrong number of results")
			}
			return failAll(err)
		}
	}
	type tally struct {
		ok, retry, invalid, noRoute, rejected int
		detail                                string
	}
	tallies := make([]tally, len(sends))
	var dead []uuid.UUID
	// Pruning guard: when most of a sizeable batch comes back invalid_token the
	// relay is probably misconfigured (an APNs sandbox/production mix-up answers
	// invalid_token for every token). Keep the devices, say so loudly.
	invalidN := 0
	for _, r := range results {
		if r.Outcome == push.InvalidToken {
			invalidN++
		}
	}
	surge := len(results) >= surgeMinResults && invalidN*2 > len(results)
	if surge {
		s.cfg.Log.WarnContext(ctx, "push relay reported most tokens invalid; devices are NOT disabled (check the relay's APNs environment)",
			"invalid", invalidN, "total", len(results), "org_id", a.OrgID)
		err := s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorKind: audit.ActorJob, Action: "push.invalid_token_surge", TargetType: "organization", TargetID: a.OrgID})
		})
		if err != nil {
			s.cfg.Log.WarnContext(ctx, "record push surge audit event", "error", err)
		}
	}
	for i, r := range results {
		t := &tallies[owners[i].send]
		switch r.Outcome {
		case push.OK:
			t.ok++
		case push.InvalidToken:
			t.invalid++
			if !surge {
				dead = append(dead, owners[i].device)
			}
		case push.NoRoute:
			t.noRoute++
			t.detail = r.Detail
		case push.Rejected:
			t.rejected++
			s.cfg.Log.WarnContext(ctx, "push message rejected by the relay; dropped", "detail", r.Detail)
		default:
			t.retry++
			t.detail = string(r.Outcome) + ": " + r.Detail
		}
	}
	if err := s.cfg.Devices.Disable(ctx, dead, "invalid_token"); err != nil {
		s.cfg.Log.WarnContext(ctx, "disable invalid push devices", "error", err)
	}
	out := make([]pushResult, len(sends))
	for i, p := range sends {
		t := tallies[i]
		switch {
		case t.ok > 0:
			out[i] = pushResult{p: p}
		case t.retry > 0:
			out[i] = pushResult{p: p, err: errors.New(strings.TrimSpace(t.detail))}
		case t.noRoute > 0:
			out[i] = pushResult{p: p, skip: "no_relay"}
		case t.rejected > 0:
			out[i] = pushResult{p: p, skip: "rejected"}
		case t.invalid > 0:
			out[i] = pushResult{p: p, skip: map[bool]string{true: "invalid_token_surge", false: "invalid_token"}[surge]}
		default:
			out[i] = pushResult{p: p, skip: "no_device"}
		}
	}
	return out
}
