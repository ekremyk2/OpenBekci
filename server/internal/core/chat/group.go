package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/opsx"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const groupProvider = "telegram"

var (
	linkCmdRe     = regexp.MustCompile(`(?i)^/link(?:@\w+)?\s+(BEKCI-[A-Z2-7]{26})\b`)
	whatsappURLRe = regexp.MustCompile(`^https://chat\.whatsapp\.com/[A-Za-z0-9]{10,64}$`)
)

// Group is a site's Telegram group as the manager sees it.
type Group struct {
	// Status: "linked", "pending" (a code was issued) or "none".
	Status      string
	Title       string
	Kind        string
	LinkedAt    *time.Time
	ExpiresAt   *time.Time
	BotUsername string
	// WhatsAppInviteURL is the invite link of the site's WhatsApp group, which residents see on Home.
	WhatsAppInviteURL *string
}

// GroupStarted is the answer to GroupStart.
type GroupStarted struct {
	Code        string
	ExpiresAt   time.Time
	BotUsername string
}

// SetInsert sets how jobs are enqueued in a transaction (the job client exists after the service).
func (s *Service) SetInsert(f func(ctx context.Context, tx pgx.Tx, args river.JobArgs) error) {
	s.insert = f
}

// SetPollSink sets the receiver of votes cast in a mirrored poll (governance).
func (s *Service) SetPollSink(p PollSink) { s.polls = p }

// SetCommands sets what the bot's commands read and write (the wiring, as the user).
func (s *Service) SetCommands(c Commands) { s.commands = c }

// RegisterWorkers registers the group post job.
func (s *Service) RegisterWorkers(w *river.Workers) { river.AddWorker(w, &groupPostWorker{s: s}) }

func (s *Service) siteScope(ctx context.Context, tx pgx.Tx, perm authz.Permission, site uuid.UUID) error {
	sc, err := opsx.SiteScope(ctx, tx, site)
	if err != nil {
		return err
	}
	return authz.Require(ctx, perm, sc)
}

func gdbErr(err error, what string) error {
	var pe *problem.Error
	if err == nil || errors.As(err, &pe) {
		return err
	}
	return problem.Internal(fmt.Errorf("%s: %w", what, err))
}

// GroupGet returns the site's Telegram group state and WhatsApp invite link (chatgroups.manage).
func (s *Service) GroupGet(ctx context.Context, org, site uuid.UUID) (Group, error) {
	var out Group
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if err := s.siteScope(ctx, tx, PermGroupManage, site); err != nil {
			return err
		}
		out = Group{Status: "none"}
		var chatID *string
		var title, kind *string
		var verified bool
		var exp *time.Time
		var at *time.Time
		err := tx.QueryRow(ctx, `SELECT chat_id, title, kind, verified, verified_at, CASE WHEN code_hash IS NOT NULL AND code_expires_at > now() THEN code_expires_at END
			FROM chat_group WHERE org_id = $1 AND site_id = $2 AND provider = 'telegram'`, org, site).Scan(&chatID, &title, &kind, &verified, &at, &exp)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			if verified {
				out.Status, out.Title, out.Kind = "linked", *title, *kind
				if at != nil {
					t := at.UTC()
					out.LinkedAt = &t
				}
			}
			if exp != nil {
				t := exp.UTC()
				out.ExpiresAt = &t
				if !verified {
					out.Status = "pending"
				}
			}
		}
		out.WhatsAppInviteURL = whatsapp(ctx, tx, org, site)
		if st, err := s.load(ctx, tx, org, groupProvider); err == nil {
			out.BotUsername = st.plain["bot_username"]
		}
		return nil
	})
	return out, gdbErr(err, "get chat group")
}

func whatsapp(ctx context.Context, tx pgx.Tx, org, site uuid.UUID) *string {
	var u *string
	_ = tx.QueryRow(ctx, `SELECT whatsapp_invite_url FROM chat_site_info WHERE org_id = $1 AND site_id = $2`, org, site).Scan(&u)
	return u
}

// WhatsAppInvite returns the site's WhatsApp group invite link for residents (any member of the organization).
func (s *Service) WhatsAppInvite(ctx context.Context, org, site uuid.UUID) (*string, error) {
	if err := authz.RequireAnywhere(ctx, "notifications.read"); err != nil {
		return nil, err
	}
	var u *string
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error { u = whatsapp(ctx, tx, org, site); return nil })
	return u, gdbErr(err, "get whatsapp invite")
}

// SetWhatsAppInvite sets or clears (nil) the site's WhatsApp group invite link (chatgroups.manage).
func (s *Service) SetWhatsAppInvite(ctx context.Context, org, site uuid.UUID, url *string) error {
	if url != nil {
		v := strings.TrimSpace(*url)
		if !whatsappURLRe.MatchString(v) {
			return problem.Validation(problem.Field{Field: "whatsapp_invite_url", Code: "invalid_format"})
		}
		url = &v
	}
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if err := s.siteScope(ctx, tx, PermGroupManage, site); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_site_info (org_id, site_id, whatsapp_invite_url, updated_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (org_id, site_id) DO UPDATE SET whatsapp_invite_url = EXCLUDED.whatsapp_invite_url, updated_at = EXCLUDED.updated_at`, org, site, url, s.cfg.Now()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, SiteID: &site, Action: "chat.whatsapp_invite_set", TargetType: "site", TargetID: site})
	})
	return gdbErr(err, "set whatsapp invite")
}

// GroupStart issues the code the manager sends as /link <code> in the group
// after adding the bot to it as an administrator (chatgroups.manage). A linked
// group stays linked until the new code is verified.
func (s *Service) GroupStart(ctx context.Context, org, site uuid.UUID) (GroupStarted, error) {
	p, err := s.provider(ctx, org, groupProvider)
	if err != nil {
		return GroupStarted{}, err
	}
	if _, ok := p.(chat.GroupBot); !ok {
		return GroupStarted{}, errNotConfigured
	}
	code, err := newCode()
	if err != nil {
		return GroupStarted{}, problem.Internal(err)
	}
	exp := s.cfg.Now().Add(codeTTL).UTC()
	var out GroupStarted
	err = s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if err := s.siteScope(ctx, tx, PermGroupManage, site); err != nil {
			return err
		}
		var gid uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO chat_group (org_id, id, site_id, provider, code_hash, code_expires_at, linked_by) VALUES ($1, $2, $3, 'telegram', $4, $5, $6)
			ON CONFLICT (org_id, site_id, provider) DO UPDATE SET code_hash = EXCLUDED.code_hash, code_expires_at = EXCLUDED.code_expires_at, linked_by = EXCLUDED.linked_by
			RETURNING id`, org, id.New(), site, hash("telegram_group", code), exp, actorID(ctx)).Scan(&gid); err != nil {
			return err
		}
		if st, err := s.load(ctx, tx, org, groupProvider); err == nil {
			out.BotUsername = st.plain["bot_username"]
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, SiteID: &site, Action: "chat.group_link_start", TargetType: "chat_group", TargetID: gid})
	})
	if err != nil {
		return GroupStarted{}, gdbErr(err, "start chat group link")
	}
	out.Code, out.ExpiresAt = code, exp
	return out, nil
}

func actorID(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

// GroupUnlink removes the site's group link and has the bot leave the group (chatgroups.manage).
func (s *Service) GroupUnlink(ctx context.Context, org, site uuid.UUID) error {
	var chatID *string
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if err := s.siteScope(ctx, tx, PermGroupManage, site); err != nil {
			return err
		}
		var gid uuid.UUID
		err := tx.QueryRow(ctx, `DELETE FROM chat_group WHERE org_id = $1 AND site_id = $2 AND provider = 'telegram' RETURNING id, CASE WHEN verified THEN chat_id END`, org, site).Scan(&gid, &chatID)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("the site has no Telegram group")
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, SiteID: &site, Action: "chat.group_unlink", TargetType: "chat_group", TargetID: gid})
	})
	if err != nil {
		return gdbErr(err, "unlink chat group")
	}
	if chatID != nil {
		if p, perr := s.provider(ctx, org, groupProvider); perr == nil {
			if gb, ok := p.(chat.GroupBot); ok {
				if lerr := gb.Leave(ctx, *chatID); lerr != nil {
					s.cfg.Log.WarnContext(ctx, "chat group: the bot could not leave", "error", lerr) // unlinked either way
				}
			}
		}
	}
	return nil
}

// GroupTitle implements jobs.GroupChannel.
func (s *Service) GroupTitle(ctx context.Context, tx pgx.Tx, org, site uuid.UUID) (string, bool, error) {
	var t string
	err := tx.QueryRow(ctx, `SELECT title FROM chat_group WHERE org_id = $1 AND site_id = $2 AND provider = 'telegram' AND verified`, org, site).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return t, err == nil, err
}

// PostToGroup implements jobs.GroupChannel: the post is a job of the business transaction.
func (s *Service) PostToGroup(ctx context.Context, tx pgx.Tx, a jobs.GroupPostArgs) error {
	if s.insert == nil {
		return errors.New("chat: the job queue is not wired")
	}
	return s.insert(ctx, tx, a)
}

var _ jobs.GroupChannel = (*Service)(nil)

type groupPostWorker struct {
	river.WorkerDefaults[jobs.GroupPostArgs]
	s *Service
}

func (w *groupPostWorker) Work(ctx context.Context, job *river.Job[jobs.GroupPostArgs]) error {
	return w.s.RunGroupPost(ctx, job.Args)
}

// RunGroupPost is the body of chat.group_post: it speaks in the site's linked
// group as the bot. A site without a group (unlinked meanwhile) is not an error.
func (s *Service) RunGroupPost(ctx context.Context, a jobs.GroupPostArgs) error {
	var gid uuid.UUID
	var chatID string
	err := s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, chat_id FROM chat_group WHERE org_id = $1 AND site_id = $2 AND provider = 'telegram' AND verified`, a.OrgID, a.SiteID).Scan(&gid, &chatID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := s.provider(ctx, a.OrgID, groupProvider)
	if err != nil {
		return nil // the bot was unconfigured meanwhile: nothing to post with
	}
	gb, ok := p.(chat.GroupBot)
	if !ok {
		return nil
	}
	err = s.post(ctx, gb, a, chatID)
	if errors.Is(err, chat.ErrDead) {
		// the bot was removed from the group: the link is gone, the manager links again
		derr := s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM chat_group WHERE org_id = $1 AND id = $2`, a.OrgID, gid); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, SiteID: &a.SiteID, ActorKind: audit.ActorSystem, Action: "chat.group_lost", TargetType: "chat_group", TargetID: gid})
		})
		return derr
	}
	if err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, SiteID: &a.SiteID, ActorKind: audit.ActorSystem, Action: "chat.group_post", TargetType: "chat_group", TargetID: gid,
			Diff: mustDiff(map[string]any{"type": a.Type})})
	})
}

func mustDiff(after map[string]any) audit.Changes {
	d, err := audit.Diff("chat_group_post", nil, after)
	if err != nil {
		return nil
	}
	return d
}

func (s *Service) post(ctx context.Context, gb chat.GroupBot, a jobs.GroupPostArgs, chatID string) error {
	switch {
	case a.Poll != nil:
		posted, err := gb.SendPoll(ctx, chatID, a.Poll.Question, a.Poll.Options)
		if err != nil {
			return err
		}
		return s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO chat_group_poll (org_id, site_id, agenda_item_id, chat_id, message_id, tg_poll_id) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
				a.OrgID, a.SiteID, a.Poll.Ref, chatID, posted.MessageID, posted.PollID)
			return err
		})
	case a.ClosePoll != nil:
		var msg int64
		var pollChat string
		err := s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `UPDATE chat_group_poll SET closed_at = $3 WHERE org_id = $1 AND agenda_item_id = $2 AND closed_at IS NULL RETURNING message_id, chat_id`,
				a.OrgID, *a.ClosePoll, s.cfg.Now()).Scan(&msg, &pollChat)
		})
		if err == nil {
			if serr := gb.StopPoll(ctx, pollChat, msg); serr != nil && !errors.Is(serr, chat.ErrDead) {
				s.cfg.Log.WarnContext(ctx, "chat group: stop poll", "error", serr) // the result is posted regardless
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		fallthrough
	default:
		if a.HTML == "" {
			return nil
		}
		posted, err := gb.SendHTML(ctx, chatID, a.HTML)
		if err != nil {
			return err
		}
		if a.Pin {
			if perr := gb.Pin(ctx, chatID, posted.MessageID); perr != nil && !errors.Is(perr, chat.ErrDead) {
				s.cfg.Log.WarnContext(ctx, "chat group: pin (the bot needs the pin right)", "error", perr)
			}
		}
		return nil
	}
}

// ---- inbound from the bot's updates

// Commands are what the bot's commands read and write, done as the linked
// user (the wiring resolves that user's rights, so a command can do nothing
// the user could not do in the app).
type Commands interface {
	// Balance describes the user's balance per unit side, one line each, and the statement's link.
	Balance(ctx context.Context, org, user uuid.UUID, locale string) (lines []string, link string, err error)
	// OpenTicket opens a ticket for the user's unit and returns its number.
	OpenTicket(ctx context.Context, org, user uuid.UUID, text string) (number string, err error)
	// Announcements lists the titles of the user's latest announcements, newest first.
	Announcements(ctx context.Context, org, user uuid.UUID, n int) ([]string, error)
}

// PollSink records a vote cast in a mirrored poll.
type PollSink interface {
	// RecordGroupVote records answer for every unit the user votes for as an
	// owner that has not answered yet, and returns how many it recorded.
	RecordGroupVote(ctx context.Context, org, item, user uuid.UUID, answer string) (applied int, err error)
}

// Update handles what a Telegram webhook carried beyond the link codes of
// private chats: /link in a group, bot commands and votes in a mirrored poll.
// A replayed update (same update_id) does nothing.
func (s *Service) Update(ctx context.Context, org uuid.UUID, gb chat.GroupBot, body []byte) error {
	u, err := gb.ParseUpdate(body)
	if err != nil {
		return nil // unreadable but authentic: not retried
	}
	if u.PollAnswer == nil && u.Text == "" {
		return nil
	}
	if u.ID != 0 {
		fresh, err := s.markSeen(ctx, org, u.ID)
		if err != nil || !fresh {
			return err
		}
	}
	err = s.handle(ctx, org, gb, u)
	if err != nil && u.ID != 0 {
		_ = s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error { // let the provider's retry run it again
			_, e := tx.Exec(ctx, `DELETE FROM chat_update WHERE org_id = $1 AND provider = 'telegram' AND update_id = $2`, org, u.ID)
			return e
		})
	}
	return err
}

func (s *Service) markSeen(ctx context.Context, org uuid.UUID, updateID int64) (bool, error) {
	var fresh bool
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_update WHERE seen_at < $1`, s.cfg.Now().Add(-72*time.Hour)); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO chat_update (org_id, provider, update_id, seen_at) VALUES ($1, 'telegram', $2, $3) ON CONFLICT DO NOTHING`, org, updateID, s.cfg.Now())
		fresh = err == nil && tag.RowsAffected() == 1
		return err
	})
	return fresh, err
}

func (s *Service) handle(ctx context.Context, org uuid.UUID, gb chat.GroupBot, u chat.Update) error {
	switch {
	case u.PollAnswer != nil:
		return s.pollVote(ctx, org, gb, *u.PollAnswer)
	case u.ChatType == "private":
		return s.command(ctx, org, gb, u)
	case u.ChatType == "group" || u.ChatType == "supergroup" || u.ChatType == "channel":
		if m := linkCmdRe.FindStringSubmatch(u.Text); m != nil {
			return s.linkGroup(ctx, org, gb, u, strings.ToUpper(m[1]))
		}
	}
	return nil
}

func (s *Service) linkGroup(ctx context.Context, org uuid.UUID, gb chat.GroupBot, u chat.Update, code string) error {
	kind := "group"
	if u.ChatType == "channel" {
		kind = "channel"
	}
	title := strings.TrimSpace(u.ChatTitle)
	if title == "" {
		title = "Telegram"
	}
	var linked bool
	var conflict bool
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		sp, err := tx.Begin(ctx) // a savepoint: a group linked to another site is a unique violation
		if err != nil {
			return err
		}
		var gid, site uuid.UUID
		var by *uuid.UUID
		err = sp.QueryRow(ctx, `UPDATE chat_group SET chat_id = $3, title = $4, kind = $5, verified = true, verified_at = $6, code_hash = NULL, code_expires_at = NULL
			WHERE org_id = $1 AND code_hash = $2 AND code_expires_at > $6 RETURNING id, site_id, linked_by`,
			org, hash("telegram_group", code), strconv.FormatInt(u.ChatID, 10), title, kind, s.cfg.Now()).Scan(&gid, &site, &by)
		var pgErr *pgconn.PgError
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_ = sp.Rollback(ctx)
			return nil // an unknown or expired code tells a stranger nothing
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			_ = sp.Rollback(ctx)
			conflict = true
			return nil
		case err != nil:
			_ = sp.Rollback(ctx)
			return err
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		linked = true
		ev := audit.Event{OrgID: org, SiteID: &site, Action: "chat.group_link", TargetType: "chat_group", TargetID: gid, Diff: mustDiff(map[string]any{"kind": kind})}
		if by != nil {
			ev.ActorKind, ev.ActorUserID = audit.ActorUser, by
		} else {
			ev.ActorKind = audit.ActorSystem
		}
		return audit.Record(ctx, tx, ev)
	})
	if err != nil {
		return err
	}
	chatID := strconv.FormatInt(u.ChatID, 10)
	switch {
	case linked:
		_, _ = gb.SendHTML(ctx, chatID, "✅ "+esc2("OpenBekci: bu grup siteye bağlandı. / This group is now linked to the site."))
	case conflict:
		_, _ = gb.SendHTML(ctx, chatID, esc2("Bu grup başka bir siteye bağlı. / This group is already linked to another site."))
	}
	return nil
}

func esc2(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// ---- polls

var pollAnswers = []string{"yes", "no", "abstain"}

type linkedUser struct {
	id     uuid.UUID
	locale string
}

// userOf finds the verified Telegram link of a chat account.
func (s *Service) userOf(ctx context.Context, org uuid.UUID, external int64) (linkedUser, bool, error) {
	var lu linkedUser
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT l.user_id, u.locale FROM chat_link l JOIN "user" u ON u.id = l.user_id
			WHERE l.org_id = $1 AND l.provider = 'telegram' AND l.external_hash = $2 AND l.verified_at IS NOT NULL`,
			org, hash("telegram", strconv.FormatInt(external, 10))).Scan(&lu.id, &lu.locale)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return lu, false, nil
	}
	return lu, err == nil, err
}

const linkFirst = "Önce OpenBekci hesabınızı bağlayın (Ayarlar, Telegram). / Link your OpenBekci account first (Settings, Telegram)."

func (s *Service) pollVote(ctx context.Context, org uuid.UUID, gb chat.GroupBot, a chat.PollAnswer) error {
	if len(a.OptionIDs) != 1 || a.OptionIDs[0] < 0 || a.OptionIDs[0] >= len(pollAnswers) {
		return nil // a withdrawn vote changes nothing
	}
	var item uuid.UUID
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT agenda_item_id FROM chat_group_poll WHERE org_id = $1 AND tg_poll_id = $2 AND closed_at IS NULL`, org, a.PollID).Scan(&item)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	dm := strconv.FormatInt(a.UserID, 10)
	lu, ok, err := s.userOf(ctx, org, a.UserID)
	if err != nil {
		return err
	}
	if !ok || s.polls == nil {
		_, _ = gb.SendHTML(ctx, dm, esc2(linkFirst)) // by DM, never in the group; fails quietly if the person never started the bot
		return nil
	}
	n, err := s.polls.RecordGroupVote(ctx, org, item, lu.id, pollAnswers[a.OptionIDs[0]])
	if err != nil {
		return err
	}
	if n == 0 {
		_, _ = gb.SendHTML(ctx, dm, esc2(pickLoc(lu.locale, "Oyunuz kaydedilmedi: yalnızca henüz yanıtlamamış bir bağımsız bölümün maliki oy verebilir.",
			"Your vote was not recorded: only the owner of a unit that has not answered yet can vote.")))
	}
	return nil
}

func pickLoc(locale, turkish, english string) string {
	if strings.HasPrefix(strings.ToLower(locale), "tr") {
		return turkish
	}
	return english
}

// ---- bot commands

func (s *Service) command(ctx context.Context, org uuid.UUID, gb chat.GroupBot, u chat.Update) error {
	text := u.Text
	if !strings.HasPrefix(text, "/") || codeRe.MatchString(text) {
		return nil // plain talk is ignored; a link code is the link flow's
	}
	dm := strconv.FormatInt(u.ChatID, 10)
	lu, ok, err := s.userOf(ctx, org, u.FromID)
	if err != nil {
		return err
	}
	if !ok || s.commands == nil {
		_, _ = gb.SendHTML(ctx, dm, esc2(linkFirst))
		return nil
	}
	word, arg, _ := strings.Cut(text, " ")
	word, _, _ = strings.Cut(strings.ToLower(word), "@")
	arg = strings.TrimSpace(arg)
	var reply string
	switch word {
	case "/borcum", "/balance":
		lines, link, err := s.commands.Balance(ctx, org, lu.id, lu.locale)
		switch {
		case err != nil:
			reply = pickLoc(lu.locale, "Bakiyeniz şu an okunamadı.", "Your balance could not be read right now.")
		case len(lines) == 0:
			reply = pickLoc(lu.locale, "Bakiye gösterilecek bir bağımsız bölümünüz yok.", "You have no unit to show a balance for.")
		default:
			reply = esc2(strings.Join(lines, "\n"))
			if link != "" {
				reply += "\n" + `<a href="` + esc2(link) + `">` + pickLoc(lu.locale, "Ekstre", "Statement") + `</a>`
			}
		}
	case "/ariza", "/issue":
		if arg == "" {
			reply = pickLoc(lu.locale, "Kullanım: /ariza <sorunun açıklaması>", "Usage: /ariza <what is wrong>")
			break
		}
		no, err := s.commands.OpenTicket(ctx, org, lu.id, arg)
		if err != nil {
			reply = pickLoc(lu.locale, "Kaydınız açılamadı. Lütfen uygulamadan deneyin.", "The request could not be opened. Please use the app.")
			break
		}
		reply = esc2(pickLoc(lu.locale, "Arıza kaydınız alındı: #"+no, "Your request was received: #"+no))
	case "/duyurular", "/announcements":
		titles, err := s.commands.Announcements(ctx, org, lu.id, 3)
		switch {
		case err != nil:
			reply = pickLoc(lu.locale, "Duyurular şu an okunamadı.", "Announcements could not be read right now.")
		case len(titles) == 0:
			reply = pickLoc(lu.locale, "Henüz duyuru yok.", "No announcements yet.")
		default:
			for i, t := range titles {
				titles[i] = fmt.Sprintf("%d. %s", i+1, t)
			}
			reply = esc2(strings.Join(titles, "\n"))
		}
	default:
		reply = esc2(pickLoc(lu.locale,
			"Komutlar:\n/borcum - bakiyem\n/ariza <metin> - arıza kaydı aç\n/duyurular - son 3 duyuru\n/yardim - bu yardım",
			"Commands:\n/borcum - my balance\n/ariza <text> - open a request\n/duyurular - the last 3 announcements\n/yardim - this help"))
	}
	_, err = gb.SendHTML(ctx, dm, reply)
	if errors.Is(err, chat.ErrDead) {
		return nil
	}
	return err
}
