package governance

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SetGroups wires the site's group chat (chat.Service); nil leaves the
// telegram_group channel and the poll mirror off.
func (s *Service) SetGroups(g jobs.GroupChannel) { s.groups = g }

// postGroup enqueues a post in the site's group. When nothing can be posted
// (no group wired or linked) an explicit request is a conflict; the poll
// mirror (explicit false) just waits.
func (s *Service) postGroup(ctx context.Context, tx pgx.Tx, org, site uuid.UUID, a jobs.GroupPostArgs, explicit bool) error {
	linked := false
	if s.groups != nil {
		var err error
		if _, linked, err = s.groups.GroupTitle(ctx, tx, org, site); err != nil {
			return err
		}
	}
	if !linked {
		if explicit {
			return problem.Conflict("no Telegram group is linked to this site")
		}
		return nil
	}
	a.OrgID, a.SiteID = org, site
	return s.groups.PostToGroup(ctx, tx, a)
}

// PollMirrorTask names the catch-up task that mirrors polls to the group.
const PollMirrorTask = "governance.poll_mirror"

// RunPollMirror posts a Telegram poll for every poll that is open and not yet
// mirrored where the site's group is linked, and, when a mirrored poll has
// closed, stops it and posts the result. Idempotent: a poll is marked in the
// same transaction that enqueues its post.
func (s *Service) RunPollMirror(ctx context.Context) error {
	if s.groups == nil || s.platform == nil {
		return nil
	}
	type due struct {
		org, item uuid.UUID
		closing   bool
	}
	var list []due
	now := s.now()
	if err := s.platform.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT p.org_id, p.agenda_item_id, false FROM poll p
			JOIN agenda_item ai ON ai.org_id = p.org_id AND ai.id = p.agenda_item_id
			JOIN meeting m ON m.org_id = ai.org_id AND m.id = ai.meeting_id
			WHERE m.status = 'called' AND p.opens_at <= $1 AND p.closes_at > $1 AND p.group_open_posted_at IS NULL
			UNION ALL
			SELECT p.org_id, p.agenda_item_id, true FROM poll p
			WHERE p.closes_at <= $1 AND p.group_open_posted_at IS NOT NULL AND p.group_close_posted_at IS NULL`, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.org, &d.item, &d.closing); err != nil {
				return err
			}
			list = append(list, d)
		}
		return rows.Err()
	}); err != nil {
		return fmt.Errorf("list polls to mirror: %w", err)
	}
	var first error
	for _, d := range list {
		err := s.db.InTenantTx(ctx, d.org, func(tx pgx.Tx) error { return s.mirrorPoll(ctx, tx, d.org, d.item, d.closing) })
		if err != nil && first == nil {
			first = fmt.Errorf("mirror poll %s: %w", d.item, err)
		}
	}
	return first
}

func (s *Service) mirrorPoll(ctx context.Context, tx pgx.Tx, org, item uuid.UUID, closing bool) error {
	var opened, closed *bool
	var question, title string
	var meetingID uuid.UUID
	var site uuid.UUID
	err := tx.QueryRow(ctx, `SELECT p.site_id, ai.meeting_id, COALESCE(p.question, ''), ai.title, p.group_open_posted_at IS NOT NULL, p.group_close_posted_at IS NOT NULL
		FROM poll p JOIN agenda_item ai ON ai.org_id = p.org_id AND ai.id = p.agenda_item_id
		WHERE p.org_id = $1 AND p.agenda_item_id = $2 FOR UPDATE OF p`, org, item).Scan(&site, &meetingID, &question, &title, &opened, &closed)
	if err != nil {
		return err
	}
	if question == "" {
		question = title
	}
	si, err := loadSiteInfo(ctx, tx, org, site)
	if err != nil {
		return err
	}
	if closing {
		if *closed || !*opened {
			return nil
		}
		m, err := loadMeeting(ctx, tx, org, meetingID, false)
		if err != nil {
			return err
		}
		var yes, no, abstain, answered int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE answer = 'yes'), count(*) FILTER (WHERE answer = 'no'), count(*) FILTER (WHERE answer = 'abstain'), count(*)
			FROM poll_answer WHERE org_id = $1 AND agenda_item_id = $2`, org, item).Scan(&yes, &no, &abstain, &answered); err != nil {
			return err
		}
		var total int64
		if snap, err := loadSnapshot(m.EligibilitySnapshot); err == nil {
			total = snap.UnitCount
		}
		if err := s.postGroup(ctx, tx, org, site, jobs.GroupPostArgs{Type: jobs.GroupPollResult, ClosePoll: &item,
			HTML: chat.PollResultHTML(si.Locale, question, yes, no, abstain, answered, total)}, false); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE poll SET group_close_posted_at = $3 WHERE org_id = $1 AND agenda_item_id = $2`, org, item, s.now())
		return err
	}
	if *opened {
		return nil
	}
	if s.groups != nil {
		if _, linked, err := s.groups.GroupTitle(ctx, tx, org, site); err != nil || !linked {
			return err // not linked yet: it is mirrored once a group is linked while the poll is open
		}
	}
	if err := s.postGroup(ctx, tx, org, site, jobs.GroupPostArgs{Type: jobs.GroupPoll,
		Poll: &jobs.GroupPollSpec{Ref: item, Question: question, Options: chat.PollOptions(si.Locale)}}, false); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE poll SET group_open_posted_at = $3 WHERE org_id = $1 AND agenda_item_id = $2`, org, item, s.now())
	return err
}

// RecordGroupVote implements chat.PollSink: a vote cast in the group's
// Telegram poll by a linked user. The poll is non-binding (G2), one answer per
// unit: the answer is recorded for each unit the user votes for as an owner
// (the eligibility snapshot) that has not answered yet, and for nothing else.
func (s *Service) RecordGroupVote(ctx context.Context, org, item, user uuid.UUID, answer string) (int, error) {
	applied := 0
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		applied = 0
		a, m, err := s.itemUnscoped(ctx, tx, org, item)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		poll, err := s.loadPoll(ctx, tx, org, item, false)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		if m.Status != StatusCalled || poll.Status != "open" {
			return nil
		}
		var person uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, org, user).Scan(&person); err != nil {
			return nil // a user without a person here votes for nothing
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return err
		}
		owner, ok := snap.Owner(person)
		if !ok {
			return nil
		}
		at := s.now().UTC()
		for _, u := range owner.Units {
			tag, err := tx.Exec(ctx, `INSERT INTO poll_answer (org_id, site_id, agenda_item_id, unit_id, answer, answered_by, answered_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (org_id, agenda_item_id, unit_id) DO NOTHING`, org, a.siteID, item, u.UnitID, answer, person, at)
			if err != nil {
				return err
			}
			applied += int(tag.RowsAffected())
		}
		if applied == 0 {
			return nil
		}
		site := a.siteID
		diff, err := audit.Diff("poll", nil, map[string]any{"answer": answer, "units": applied, "via": "telegram"})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, SiteID: &site, ActorKind: audit.ActorSystem, Action: "poll.answer", TargetType: "agenda_item", TargetID: item, Diff: diff})
	})
	return applied, err
}
