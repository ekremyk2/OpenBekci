package governance

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// VoteCount is the in-room count of an agenda item with the threshold
// evaluation (a hint: the manager still records the decision). The counts are
// what the room voted; pre-meeting polls never enter them (G2).
type VoteCount struct {
	AgendaItemID uuid.UUID  `json:"agenda_item_id"`
	YesCount     int64      `json:"yes_count"`
	NoCount      int64      `json:"no_count"`
	AbstainCount int64      `json:"abstain_count"`
	YesShare     *float64   `json:"yes_share"`
	NoShare      *float64   `json:"no_share"`
	AbstainShare *float64   `json:"abstain_share"`
	RecordedBy   *uuid.UUID `json:"recorded_by"`
	RecordedAt   *time.Time `json:"recorded_at"`
	OutcomeHint  string     `json:"outcome_hint"`
}

// VoteCountSet is the input of RecordVoteCounts. Shares are in the same unit
// as the eligibility snapshot's shares (arsa payi). When omitted they are
// estimated from the average share of those present, which is only exact when
// every unit has the same share.
type VoteCountSet struct {
	YesCount     int64    `json:"yes_count"`
	NoCount      int64    `json:"no_count"`
	AbstainCount int64    `json:"abstain_count"`
	YesShare     *float64 `json:"yes_share"`
	NoShare      *float64 `json:"no_share"`
	AbstainShare *float64 `json:"abstain_share"`
}

// evaluate applies the item's topic rule to counts.
func (s *Service) evaluate(ctx context.Context, tx pgx.Tx, m Meeting, a AgendaItem, c Counts) (string, error) {
	snap, err := loadSnapshot(m.EligibilitySnapshot)
	if err != nil {
		return HintUndetermined, nil
	}
	set := m.rules()
	pr, err := loadPresence(ctx, tx, m.OrgID, m.ID, m.heldCall(), snap, set)
	if err != nil {
		return "", err
	}
	topic := DefaultTopic
	if a.Topic != nil {
		topic = *a.Topic
	}
	rule := set.RuleFor(topic)
	return EvalThreshold(rule.Rule, c, Basis{Eligible: snap.Total(), Present: pr.Capped, Call: m.heldCall(), NoQuorumSecondCall: !set.Quorum.SecondCallRequiresQuorum}), nil
}

func loadVoteCount(ctx context.Context, tx pgx.Tx, orgID, itemID uuid.UUID) (VoteCount, Counts, bool, error) {
	v := VoteCount{AgendaItemID: itemID, OutcomeHint: HintUndetermined}
	var c Counts
	var ys, ns, as float64
	err := tx.QueryRow(ctx, `SELECT yes_count, no_count, abstain_count, yes_share::float8, no_share::float8, abstain_share::float8, recorded_by, recorded_at
		FROM vote_count WHERE org_id = $1 AND agenda_item_id = $2`, orgID, itemID).Scan(&v.YesCount, &v.NoCount, &v.AbstainCount, &ys, &ns, &as, &v.RecordedBy, &v.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, c, false, nil
	}
	if err != nil {
		return v, c, false, err
	}
	at := v.RecordedAt.UTC()
	v.RecordedAt, v.YesShare, v.NoShare, v.AbstainShare = &at, &ys, &ns, &as
	c = Counts{Yes: v.YesCount, No: v.NoCount, Abstain: v.AbstainCount, YesShare: ys, NoShare: ns, AbstainShare: as}
	return v, c, true, nil
}

func (s *Service) voteCountOf(ctx context.Context, tx pgx.Tx, m Meeting, a AgendaItem) (VoteCount, error) {
	v, c, ok, err := loadVoteCount(ctx, tx, m.OrgID, a.ID)
	if err != nil || !ok {
		return v, err
	}
	v.OutcomeHint, err = s.evaluate(ctx, tx, m, a, c)
	return v, err
}

// GetVoteCounts returns the recorded counts of an item (zeros and an
// undetermined hint when nothing is recorded). It needs meetings.read; only
// owners and staff see it.
func (s *Service) GetVoteCounts(ctx context.Context, orgID, itemID uuid.UUID) (VoteCount, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return VoteCount{}, err
	}
	var out VoteCount
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, sc, err := s.itemAt(ctx, tx, PermMeetingsRead, orgID, itemID, false)
		if err != nil {
			return err
		}
		if v := viewerOf(ctx, sc); !v.staff && !v.owner {
			return problem.Forbidden("vote counts are shown to owners")
		}
		out, err = s.voteCountOf(ctx, tx, m, a)
		return err
	})
	return out, dbErr(err, "vote counts")
}

// RecordVoteCounts records (or corrects) the counts the room voted on an
// agenda item. The quorum must be reached, the counts cannot exceed those
// present, and the outcome hint is the site's threshold rule for the item's
// topic applied to them. It needs decisions.manage.
func (s *Service) RecordVoteCounts(ctx context.Context, orgID, itemID uuid.UUID, in VoteCountSet) (VoteCount, error) {
	if err := authz.RequireAnywhere(ctx, PermDecisionsManage); err != nil {
		return VoteCount{}, err
	}
	var fs []problem.Field
	for name, v := range map[string]int64{"yes_count": in.YesCount, "no_count": in.NoCount, "abstain_count": in.AbstainCount} {
		if v < 0 {
			fs = append(fs, problem.Field{Field: name, Code: "invalid_value"})
		}
	}
	for name, v := range map[string]*float64{"yes_share": in.YesShare, "no_share": in.NoShare, "abstain_share": in.AbstainShare} {
		if v != nil && *v < 0 {
			fs = append(fs, problem.Field{Field: name, Code: "invalid_value"})
		}
	}
	if len(fs) > 0 {
		return VoteCount{}, problem.Validation(fs...)
	}
	var out VoteCount
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, sc, err := s.itemAt(ctx, tx, PermDecisionsManage, orgID, itemID, true)
		if err != nil {
			return err
		}
		if m.Status != StatusInSession && m.Status != StatusClosed {
			return problem.Conflict("votes are counted in the session")
		}
		if !a.WillBeVoted {
			return problem.Conflict("this agenda item is not marked for a vote")
		}
		q, _, pr, _, err := s.quorumOf(ctx, tx, m)
		if err != nil {
			return err
		}
		if !q.Reached {
			return problem.Conflict("the quorum is not reached; no binding vote can be recorded")
		}
		total := in.YesCount + in.NoCount + in.AbstainCount
		if total > pr.Capped.Votes {
			return problem.Validation(problem.Field{Field: "yes_count", Code: "invalid_value", Params: map[string]any{"reason": "more_votes_than_present", "present": pr.Capped.Votes}})
		}
		avg := 0.0
		if pr.Capped.Votes > 0 {
			avg = pr.Capped.Share / float64(pr.Capped.Votes)
		}
		share := func(given *float64, count int64) float64 {
			if given != nil {
				return *given
			}
			return avg * float64(count)
		}
		ys, ns, as := share(in.YesShare, in.YesCount), share(in.NoShare, in.NoCount), share(in.AbstainShare, in.AbstainCount)
		if ys+ns+as > pr.Capped.Share*(1+1e-9)+1e-9 {
			return problem.Validation(problem.Field{Field: "yes_share", Code: "invalid_value", Params: map[string]any{"reason": "more_share_than_present"}})
		}
		before, _, had, err := loadVoteCount(ctx, tx, orgID, itemID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO vote_count (org_id, site_id, agenda_item_id, yes_count, no_count, abstain_count, yes_share, no_share, abstain_share, recorded_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (org_id, agenda_item_id) DO UPDATE SET yes_count = $4, no_count = $5, abstain_count = $6, yes_share = $7, no_share = $8, abstain_share = $9,
				recorded_by = $10, recorded_at = now()`, orgID, a.siteID, itemID, in.YesCount, in.NoCount, in.AbstainCount, ys, ns, as, principalID(ctx)); err != nil {
			return err
		}
		if out, err = s.voteCountOf(ctx, tx, m, a); err != nil {
			return err
		}
		var bv any
		if had {
			bv = map[string]any{"yes_count": before.YesCount, "no_count": before.NoCount, "abstain_count": before.AbstainCount}
		}
		diff, err := audit.Diff("vote_count", bv, map[string]any{"yes_count": out.YesCount, "no_count": out.NoCount, "abstain_count": out.AbstainCount, "outcome_hint": out.OutcomeHint})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "vote_count.record", TargetType: "agenda_item", TargetID: itemID, Diff: diff})
	})
	return out, dbErr(err, "vote counts")
}
