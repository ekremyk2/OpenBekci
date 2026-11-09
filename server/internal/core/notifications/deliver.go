package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbekci/openbekci/server/internal/platform/obs"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	chatcore "github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// maxAttempts matches the job's MaxAttempts: after the last try a failed
// delivery stays failed and the job ends.
const maxAttempts = 8

type contact struct {
	person       uuid.UUID
	userID       *uuid.UUID
	email, phone string
	locale       string
}

type pendingSend struct {
	deliveryID     uuid.UUID
	notificationID uuid.UUID
	channel        string
	attempts       int
	person         uuid.UUID
	text           Rendered
	link           string
}

// Deliver runs one notifications.deliver job: it resolves the recipients,
// drops those who muted the type, writes the in-app rows and then sends email
// and SMS, recording every delivery. It is safe to run twice (River retries):
// rows are keyed by the job's Key and only unfinished deliveries are sent
// again.
func (s *Service) Deliver(ctx context.Context, a jobs.NotifyArgs, attempt int) error {
	if a.OrgID == uuid.Nil || a.Type == "" || a.Key == "" {
		return nil // malformed: retrying cannot help
	}
	var sends []pendingSend
	var siteName string
	err := s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		var orgLocale string
		if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, a.OrgID).Scan(&orgLocale); err != nil {
			return err
		}
		if a.SiteID != nil {
			if err := tx.QueryRow(ctx, `SELECT name FROM site WHERE org_id = $1 AND id = $2`, a.OrgID, *a.SiteID).Scan(&siteName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		people, err := s.resolve(ctx, tx, a, orgLocale)
		if err != nil {
			return err
		}
		channels := NormalizeChannels(a.Channels)
		if len(channels) == 0 {
			channels = DefaultChannels(a.Type)
			if a.SiteID != nil {
				if err := tx.QueryRow(ctx, `SELECT channels FROM site_channel_default WHERE org_id = $1 AND site_id = $2 AND type = $3`,
					a.OrgID, *a.SiteID, a.Type).Scan(&channels); err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
		}
		muted := map[uuid.UUID]bool{}
		if !a.Unmutable && !IsUnmutable(a.Type) {
			ids := make([]uuid.UUID, len(people))
			for i, p := range people {
				ids[i] = p.person
			}
			rows, err := tx.Query(ctx, `SELECT person_id FROM notification_mute WHERE org_id = $1 AND type = $2 AND person_id = ANY($3)`, a.OrgID, a.Type, ids)
			if err != nil {
				return err
			}
			for rows.Next() {
				var p uuid.UUID
				if err := rows.Scan(&p); err != nil {
					rows.Close()
					return err
				}
				muted[p] = true
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		// who has an enabled push device: read straight from the (instance-wide)
		// device table on this connection; the tokens are opened later, outside
		// this transaction
		withDevice := map[uuid.UUID]bool{}
		if s.pushEnabled() {
			var uids []uuid.UUID
			for _, p := range people {
				if p.userID != nil {
					uids = append(uids, *p.userID)
				}
			}
			rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM device WHERE user_id = ANY($1) AND disabled_at IS NULL`, uids)
			if err != nil {
				return err
			}
			has := map[uuid.UUID]bool{}
			for rows.Next() {
				var u uuid.UUID
				if err := rows.Scan(&u); err != nil {
					rows.Close()
					return err
				}
				has[u] = true
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, p := range people {
				withDevice[p.person] = p.userID != nil && has[*p.userID]
			}
		}
		// who can be reached through a linked chat (verified link, session window)
		var reach map[uuid.UUID]chatcore.Reach
		if s.cfg.Chat != nil && slices.Contains(channels, jobs.ChannelChat) {
			var uids []uuid.UUID
			for _, p := range people {
				if p.userID != nil {
					uids = append(uids, *p.userID)
				}
			}
			if reach, err = s.cfg.Chat.Reaches(ctx, tx, a.OrgID, uids); err != nil {
				return err
			}
		}
		payload := map[string]any{}
		for k, v := range a.Payload {
			payload[k] = v
		}
		if _, ok := payload["site"]; !ok {
			payload["site"] = siteName
		}
		raw, _ := json.Marshal(a.Payload)
		if a.Payload == nil {
			raw = []byte(`{}`)
		}
		for _, p := range people {
			if muted[p.person] {
				continue
			}
			nid := id.New()
			var link *string
			if a.Link != "" {
				link = &a.Link
			}
			tag, err := tx.Exec(ctx, `INSERT INTO notification (id, org_id, site_id, person_id, type, payload, link, dedupe_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (org_id, person_id, type, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`,
				nid, a.OrgID, a.SiteID, p.person, a.Type, raw, link, a.Key)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				chs := channels
				if IsUnmutable(a.Type) && len(NormalizeChannels(a.Channels)) == 0 {
					chs = []string{UnmutableChannel(p.phone != "")} // sign-in codes and invitations: one address, never both
				}
				// push accompanies the in-app copy of every automatic message for a
				// person with a device (the app then fetches the notification);
				// it is also a channel a sender or site can ask for explicitly
				if !IsUnmutable(a.Type) && slices.Contains(chs, jobs.ChannelInApp) && !slices.Contains(chs, jobs.ChannelPush) && withDevice[p.person] {
					chs = append(slices.Clone(chs), jobs.ChannelPush)
				}
				var fallbacks []string
				for _, ch := range chs {
					status, why := "pending", ""
					switch {
					case ch == jobs.ChannelInApp:
						status = "sent"
					case ch == jobs.ChannelEmail && (p.email == "" || s.cfg.Mail == nil):
						status, why = "skipped", skipReason(p.email == "", "no_address", "mail_unavailable")
					case ch == jobs.ChannelSMS && (p.phone == "" || s.cfg.SMS == nil):
						status, why = "skipped", skipReason(p.phone == "", "no_address", "sms_unavailable")
					case ch == jobs.ChannelPush && !s.pushEnabled():
						status, why = "skipped", "push_unavailable"
					case ch == jobs.ChannelPush && !withDevice[p.person]:
						status, why = "skipped", "no_device"
					case ch == jobs.ChannelChat && s.cfg.Chat == nil:
						status, why = "skipped", "chat_unavailable"
					case ch == jobs.ChannelChat && (p.userID == nil || reach[*p.userID] == chatcore.ReachNone):
						status, why = "skipped", "no_link"
					case ch == jobs.ChannelChat && reach[*p.userID] == chatcore.ReachWindowClosed:
						status, why = "skipped", "window_closed"
						// the session window is over: fall back to the person's next channel
						// (SMS, else email) unless the message already goes there
						if fb := s.chatFallback(chs, p); fb != "" {
							fallbacks = append(fallbacks, fb)
						}
					}
					if _, err := tx.Exec(ctx, `INSERT INTO notification_delivery (id, org_id, notification_id, channel, status, last_error)
						VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))`, id.New(), a.OrgID, nid, ch, status, why); err != nil {
						return err
					}
				}
				for _, ch := range slices.Compact(fallbacks) {
					if _, err := tx.Exec(ctx, `INSERT INTO notification_delivery (id, org_id, notification_id, channel, status)
						VALUES ($1, $2, $3, $4, 'pending')`, id.New(), a.OrgID, nid, ch); err != nil {
						return err
					}
				}
			}
		}
		// Claim every unfinished email and SMS delivery of this event (first try or
		// retry). The claim is one atomic UPDATE: two workers running the same event
		// at once never get the same row, so nothing is sent twice. A claim that a
		// crashed worker left behind is taken over after ten minutes.
		rows, err := tx.Query(ctx, `UPDATE notification_delivery d SET status = 'sending', claimed_at = now(), updated_at = now()
			FROM notification n
			WHERE n.org_id = d.org_id AND n.id = d.notification_id AND n.org_id = $1 AND n.dedupe_key = $2 AND n.type = $4
			  AND d.channel IN ('email', 'sms', 'push', 'chat') AND d.attempts < $3
			  AND (d.status IN ('pending', 'failed') OR (d.status = 'sending' AND d.claimed_at < now() - interval '10 minutes'))
			RETURNING d.id, d.channel, d.attempts, n.person_id, n.link, n.id`, a.OrgID, a.Key, maxAttempts, a.Type)
		if err != nil {
			return err
		}
		defer rows.Close()
		byPerson := map[uuid.UUID]contact{}
		for _, p := range people {
			byPerson[p.person] = p
		}
		cache := map[string]Rendered{}
		for rows.Next() {
			var ps pendingSend
			var link *string
			if err := rows.Scan(&ps.deliveryID, &ps.channel, &ps.attempts, &ps.person, &link, &ps.notificationID); err != nil {
				return err
			}
			c, ok := byPerson[ps.person]
			if !ok {
				continue
			}
			if link != nil {
				ps.link = *link
			}
			r, ok := cache[c.locale]
			if !ok {
				r = Render(a.Type, c.locale, payload)
				cache[c.locale] = r
			}
			ps.text = r
			sends = append(sends, ps)
		}
		return rows.Err()
	})
	if err != nil {
		return fmt.Errorf("prepare notification: %w", err)
	}
	if len(sends) == 0 {
		return nil
	}
	// contacts again by person, outside the first transaction's closure
	contacts := map[uuid.UUID]contact{}
	err = s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		ps := make([]uuid.UUID, 0, len(sends))
		for _, p := range sends {
			ps = append(ps, p.person)
		}
		rows, err := tx.Query(ctx, personQuery+` WHERE p.org_id = $1 AND p.id = ANY($2)`, a.OrgID, ps)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanContact(rows)
			if err != nil {
				return err
			}
			contacts[c.person] = c
		}
		return rows.Err()
	})
	if err != nil {
		return fmt.Errorf("load contacts: %w", err)
	}
	type result struct {
		p        pendingSend
		err      error
		skip     string // set: the delivery is recorded as skipped for this reason
		provider string
		segments int
	}
	var results []result
	var pushSends []pendingSend
	for _, p := range sends {
		c := contacts[p.person]
		r := result{p: p}
		switch p.channel {
		case jobs.ChannelPush:
			pushSends = append(pushSends, p)
			continue
		case jobs.ChannelChat:
			if c.userID == nil {
				r.skip = "no_link"
				break
			}
			// content-minimal: the title (with the site) and the link, no body
			title := p.text.Title
			if siteName != "" {
				title = siteName + ": " + title
			}
			m := chat.Message{Text: title, Label: openLabel(c.locale)}
			if p.link != "" && s.cfg.BaseURL != "" {
				m.Link = strings.TrimRight(s.cfg.BaseURL, "/") + p.link
			}
			switch err := s.cfg.Chat.Send(ctx, a.OrgID, *c.userID, m); {
			case err == nil:
			case errors.Is(err, chatcore.ErrNoLink):
				r.skip = "no_link"
			case errors.Is(err, chat.ErrWindowClosed):
				r.skip = "window_closed"
			case errors.Is(err, chat.ErrDead):
				r.skip = "link_dead"
			default:
				r.err = err
			}
		case jobs.ChannelEmail:
			body := p.text.Body
			if p.link != "" && s.cfg.BaseURL != "" {
				body += "\n\n" + strings.TrimRight(s.cfg.BaseURL, "/") + p.link
			}
			r.err = s.cfg.Mail.Send(ctx, mail.Message{To: c.email, Subject: p.text.Subject, Body: body})
		case jobs.ChannelSMS:
			r.provider, r.err = s.cfg.SMS.Send(sms.WithOrg(ctx, a.OrgID), c.phone, p.text.SMS)
			if r.err == nil {
				r.segments = SMSSegments(p.text.SMS)
			}
		}
		results = append(results, r)
	}
	if len(pushSends) > 0 {
		for _, x := range s.deliverPush(ctx, a, pushSends, contacts) {
			results = append(results, result{p: x.p, err: x.err, skip: x.skip})
		}
	}
	var failed int
	err = s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		var msgs, segs int
		for _, r := range results {
			if r.skip != "" {
				obs.Notification(r.p.channel, "skipped")
				if _, err := tx.Exec(ctx, `UPDATE notification_delivery SET status = 'skipped', attempts = attempts + 1, last_error = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
					a.OrgID, r.p.deliveryID, truncate(r.skip, 300)); err != nil {
					return err
				}
				continue
			}
			if r.err != nil {
				failed++
				obs.Notification(r.p.channel, "failed")
				if _, err := tx.Exec(ctx, `UPDATE notification_delivery SET status = 'failed', attempts = attempts + 1, last_error = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
					a.OrgID, r.p.deliveryID, truncate(r.err.Error(), 300)); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE notification_delivery SET status = 'sent', attempts = attempts + 1, last_error = NULL, provider_id = NULLIF($3, ''), sms_segments = $4, updated_at = now() WHERE org_id = $1 AND id = $2`,
				a.OrgID, r.p.deliveryID, r.provider, r.segments); err != nil {
				return err
			}
			obs.Notification(r.p.channel, "sent")
			if r.p.channel == jobs.ChannelSMS {
				msgs++
				segs += r.segments
			}
		}
		if msgs > 0 && a.SiteID != nil {
			first, err := s.siteMonth(ctx, tx, a.OrgID, *a.SiteID, s.nowTime())
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO sms_usage (org_id, site_id, month, messages, segments) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (org_id, site_id, month) DO UPDATE SET messages = sms_usage.messages + EXCLUDED.messages, segments = sms_usage.segments + EXCLUDED.segments`,
				a.OrgID, *a.SiteID, first, msgs, segs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record deliveries: %w", err)
	}
	if failed > 0 && attempt < maxAttempts {
		return fmt.Errorf("%d deliveries failed", failed)
	}
	return nil
}

// chatFallback is the channel a message takes when the chat's session window is
// over: SMS when the person has a number, else email, and only when the message
// does not go there anyway. Empty means no fallback.
func (s *Service) chatFallback(chs []string, p contact) string {
	switch {
	case slices.Contains(chs, jobs.ChannelSMS) || slices.Contains(chs, jobs.ChannelEmail):
		return ""
	case p.phone != "" && s.cfg.SMS != nil:
		return jobs.ChannelSMS
	case p.email != "" && s.cfg.Mail != nil:
		return jobs.ChannelEmail
	}
	return ""
}

// openLabel is the text of the link button of a chat message.
func openLabel(locale string) string {
	if locale == "tr" {
		return "Aç"
	}
	return "Open"
}

// nowTime is the service clock.
func (s *Service) nowTime() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// siteMonth is the first day of the month containing t in the site's time
// zone, as the date the SMS counter is keyed by. Writing and reading the
// counter both go through it.
func (s *Service) siteMonth(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, t time.Time) (time.Time, error) {
	var tz string
	if err := tx.QueryRow(ctx, `SELECT timezone FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&tz); err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	n := t.In(loc)
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC), nil
}

func skipReason(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

const personQuery = `SELECT p.id, p.user_id, coalesce(p.contact->>'email', u.email::text, ''), coalesce(p.contact->>'phone', u.phone, ''), coalesce(u.locale, '')
	FROM person p LEFT JOIN "user" u ON u.id = p.user_id`

func scanContact(row pgx.Row) (contact, error) {
	var c contact
	err := row.Scan(&c.person, &c.userID, &c.email, &c.phone, &c.locale)
	return c, err
}

// resolve turns the job's recipients into people of the organization with
// their addresses and language.
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, a jobs.NotifyArgs, orgLocale string) ([]contact, error) {
	var pids, uids []uuid.UUID
	for _, r := range a.Recipients {
		if r.PersonID != nil {
			pids = append(pids, *r.PersonID)
		}
		if r.UserID != nil {
			uids = append(uids, *r.UserID)
		}
	}
	rows, err := tx.Query(ctx, personQuery+` WHERE p.org_id = $1 AND (p.id = ANY($2) OR p.user_id = ANY($3))`, a.OrgID, pids, uids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contact
	seen := map[uuid.UUID]bool{}
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		if seen[c.person] {
			continue
		}
		seen[c.person] = true
		switch {
		case a.Locale != "" && NormalizeLocale(a.Locale) != "":
			c.locale = NormalizeLocale(a.Locale)
		case NormalizeLocale(c.locale) != "":
			c.locale = NormalizeLocale(c.locale)
		default:
			c.locale = NormalizeLocale(orgLocale)
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(x, y contact) int { return strings.Compare(x.person.String(), y.person.String()) })
	return out, rows.Err()
}
