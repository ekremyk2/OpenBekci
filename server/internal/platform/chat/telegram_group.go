package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Update is one Telegram update as the group features read it.
type Update struct {
	ID int64
	// Message fields (message or channel_post).
	ChatID    int64
	ChatType  string // private, group, supergroup, channel
	ChatTitle string
	FromID    int64
	Text      string
	// PollAnswer is set for a vote on a non-anonymous poll.
	PollAnswer *PollAnswer
}

// PollAnswer is a vote on a native poll; no OptionIDs means the vote was withdrawn.
type PollAnswer struct {
	PollID    string
	UserID    int64
	OptionIDs []int
}

// Posted is what Telegram says about a message it accepted.
type Posted struct {
	MessageID int64
	PollID    string // sendPoll only
}

// GroupBot is implemented by providers that can speak in a group (Telegram).
type GroupBot interface {
	ParseUpdate(body []byte) (Update, error)
	// SendHTML sends text in Telegram's HTML subset (the caller escapes).
	SendHTML(ctx context.Context, chatID, html string) (Posted, error)
	Pin(ctx context.Context, chatID string, messageID int64) error
	SendPoll(ctx context.Context, chatID, question string, options []string) (Posted, error)
	StopPoll(ctx context.Context, chatID string, messageID int64) error
	Leave(ctx context.Context, chatID string) error
}

func (t *telegram) ParseUpdate(body []byte) (Update, error) {
	type chatObj struct {
		ID    int64  `json:"id"`
		Type  string `json:"type"`
		Title string `json:"title"`
	}
	type msg struct {
		Text string  `json:"text"`
		Chat chatObj `json:"chat"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
	}
	var u struct {
		UpdateID    int64 `json:"update_id"`
		Message     *msg  `json:"message"`
		ChannelPost *msg  `json:"channel_post"`
		PollAnswer  *struct {
			PollID string `json:"poll_id"`
			User   struct {
				ID int64 `json:"id"`
			} `json:"user"`
			OptionIDs []int `json:"option_ids"`
		} `json:"poll_answer"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return Update{}, fmt.Errorf("telegram: update: %w", err)
	}
	out := Update{ID: u.UpdateID}
	if m := u.Message; m != nil {
		out.ChatID, out.ChatType, out.ChatTitle, out.FromID, out.Text = m.Chat.ID, m.Chat.Type, m.Chat.Title, m.From.ID, strings.TrimSpace(m.Text)
	} else if m := u.ChannelPost; m != nil {
		out.ChatID, out.ChatType, out.ChatTitle, out.Text = m.Chat.ID, m.Chat.Type, m.Chat.Title, strings.TrimSpace(m.Text)
	}
	if a := u.PollAnswer; a != nil {
		out.PollAnswer = &PollAnswer{PollID: a.PollID, UserID: a.User.ID, OptionIDs: a.OptionIDs}
	}
	return out, nil
}

// do calls a method and returns its result object; 403 and "chat not found"
// are ErrDead, 429 and 5xx retryable.
func (t *telegram) do(ctx context.Context, method string, in any) (json.RawMessage, error) {
	status, raw, err := t.call(ctx, method, in)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusOK:
		var r struct {
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal(raw, &r)
		return r.Result, nil
	case status == http.StatusForbidden || (status == http.StatusBadRequest && strings.Contains(string(raw), "chat not found")):
		return nil, ErrDead
	case status == http.StatusTooManyRequests || status >= 500:
		return nil, fmt.Errorf("telegram: %s: HTTP %d (retryable)", method, status)
	}
	return nil, fmt.Errorf("telegram: %s rejected: HTTP %d: %s", method, status, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
}

func (t *telegram) SendHTML(ctx context.Context, chatID, html string) (Posted, error) {
	res, err := t.do(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": html, "parse_mode": "HTML", "disable_web_page_preview": true})
	if err != nil {
		return Posted{}, err
	}
	var m struct {
		MessageID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(res, &m)
	return Posted{MessageID: m.MessageID}, nil
}

func (t *telegram) Pin(ctx context.Context, chatID string, messageID int64) error {
	_, err := t.do(ctx, "pinChatMessage", map[string]any{"chat_id": chatID, "message_id": messageID})
	return err
}

// SendPoll opens a regular poll with named (not anonymous) votes and one answer.
func (t *telegram) SendPoll(ctx context.Context, chatID, question string, options []string) (Posted, error) {
	res, err := t.do(ctx, "sendPoll", map[string]any{"chat_id": chatID, "question": clip(question, 300), "options": clipAll(options, 100),
		"is_anonymous": false, "allows_multiple_answers": false})
	if err != nil {
		return Posted{}, err
	}
	var m struct {
		MessageID int64 `json:"message_id"`
		Poll      struct {
			ID string `json:"id"`
		} `json:"poll"`
	}
	_ = json.Unmarshal(res, &m)
	return Posted{MessageID: m.MessageID, PollID: m.Poll.ID}, nil
}

func (t *telegram) StopPoll(ctx context.Context, chatID string, messageID int64) error {
	_, err := t.do(ctx, "stopPoll", map[string]any{"chat_id": chatID, "message_id": messageID})
	return err
}

func (t *telegram) Leave(ctx context.Context, chatID string) error {
	_, err := t.do(ctx, "leaveChat", map[string]any{"chat_id": chatID})
	return err
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func clipAll(in []string, n int) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = clip(s, n)
	}
	return out
}
