package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// ChannelTelegramGroup is the sender-chosen channel that posts once in the
// site's Telegram group (the owner of the message is the site, not a person).
// It is not a notification channel: the dispatcher never sees it.
const ChannelTelegramGroup = "telegram_group"

// Types of a group post.
const (
	GroupAnnouncement = "announcement"
	GroupMeetingCall  = "meeting_call"
	GroupDues         = "dues_reminder"
	GroupPoll         = "poll"
	GroupPollResult   = "poll_result"
)

// GroupPollSpec asks for a native Telegram poll mirroring a poll of OpenBekci.
type GroupPollSpec struct {
	Ref      uuid.UUID `json:"ref"`
	Question string    `json:"question"`
	Options  []string  `json:"options"`
}

// GroupPostArgs is the arguments of chat.group_post.
type GroupPostArgs struct {
	OrgID  uuid.UUID `json:"org_id"`
	SiteID uuid.UUID `json:"site_id"`
	Type   string    `json:"type"`
	// HTML is the message in Telegram's HTML subset, already escaped.
	HTML string `json:"html,omitempty"`
	Pin  bool   `json:"pin,omitempty"`
	// Poll opens a native poll (HTML is not used).
	Poll *GroupPollSpec `json:"poll,omitempty"`
	// ClosePoll stops the poll mirrored for that reference, then posts HTML.
	ClosePoll *uuid.UUID `json:"close_poll,omitempty"`
}

// Kind implements river.JobArgs.
func (GroupPostArgs) Kind() string { return "chat.group_post" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (GroupPostArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 6} }

// GroupChannel is the site's group chat as the modules that post see it
// (*chat.Service). Nil in a module means the channel is not available.
type GroupChannel interface {
	// GroupTitle is the title of the site's linked group; ok is false when none is linked.
	GroupTitle(ctx context.Context, tx pgx.Tx, org, site uuid.UUID) (title string, ok bool, err error)
	// PostToGroup enqueues the post in the business transaction.
	PostToGroup(ctx context.Context, tx pgx.Tx, a GroupPostArgs) error
}
