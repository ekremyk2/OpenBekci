package chat

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// The texts the bot posts in a site's group, in Telegram's HTML subset. Every
// piece of user-written text is escaped first; only the tags built here
// survive. A group is a public room: nothing in these texts names a person,
// an amount owed, a unit's balance, a parcel or a visitor (KVKK).

const (
	maxBodyRunes = 3000 // a message takes 4096 characters; leave room for the frame
	maxAgenda    = 15
)

var (
	mdLink   = regexp.MustCompile(`\[([^\]\n]{1,200})\]\((https?://[^)\s]{1,500})\)`)
	mdBold   = regexp.MustCompile(`\*\*([^*\n]{1,500})\*\*`)
	mdCode   = regexp.MustCompile("`([^`\n]{1,300})`")
	mdHeader = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
)

func tr(locale string) bool { return strings.HasPrefix(strings.ToLower(locale), "tr") }

func esc(s string) string { return html.EscapeString(s) }

func pick(locale, turkish, english string) string {
	if tr(locale) {
		return turkish
	}
	return english
}

func openLink(url, label string) string {
	return `<a href="` + esc(url) + `">` + esc(label) + `</a>`
}

// MarkdownHTML renders the small Markdown subset announcements use (headings,
// bold, inline code, links) as Telegram HTML; everything else is plain text.
func MarkdownHTML(md string) string {
	if r := []rune(md); len(r) > maxBodyRunes {
		md = string(r[:maxBodyRunes]) + "…"
	}
	s := esc(md)
	s = mdLink.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = mdHeader.ReplaceAllString(s, `<b>$1</b>`)
	s = mdBold.ReplaceAllString(s, `<b>$1</b>`)
	s = mdCode.ReplaceAllString(s, `<code>$1</code>`)
	return s
}

// AnnouncementHTML is an announcement as a group post: title, body, the
// attachments' names and a link back to the announcement.
func AnnouncementHTML(locale, title, bodyMD, link string, attachments []string) string {
	var b strings.Builder
	b.WriteString("<b>" + esc(title) + "</b>\n\n" + MarkdownHTML(bodyMD))
	if len(attachments) > 0 {
		b.WriteString("\n\n")
		for _, a := range attachments {
			b.WriteString("📎 " + openLink(link, a) + "\n")
		}
	}
	if link != "" {
		b.WriteString("\n" + openLink(link, pick(locale, "Duyuruyu aç", "Open the announcement")))
	}
	return strings.TrimSpace(b.String())
}

// MeetingCallHTML is the call of a meeting: date, place, agenda and a link.
func MeetingCallHTML(locale, site, title, when, place string, agenda []string, link string) string {
	head := pick(locale, "Toplantı çağrısı", "Meeting call")
	var b strings.Builder
	b.WriteString("<b>" + esc(head+": "+site) + "</b>\n")
	if title != "" {
		b.WriteString(esc(title) + "\n")
	}
	b.WriteString("\n🗓 " + esc(when) + "\n📍 " + esc(place) + "\n")
	if len(agenda) > 0 {
		b.WriteString("\n<b>" + pick(locale, "Gündem", "Agenda") + "</b>\n")
		for i, a := range agenda {
			if i == maxAgenda {
				b.WriteString("…\n")
				break
			}
			fmt.Fprintf(&b, "%d. %s\n", i+1, esc(a))
		}
	}
	if link != "" {
		b.WriteString("\n" + openLink(link, pick(locale, "Çağrıyı aç", "Open the call")))
	}
	return strings.TrimSpace(b.String())
}

// DuesHTML is the only payment reminder a group ever sees: it says a reminder
// went out and sends everyone to their own statement; it carries no name and no amount.
func DuesHTML(locale, site, link string) string {
	s := "<b>" + esc(site) + "</b>\n" + pick(locale,
		"Aidat hatırlatması gönderildi. Lütfen hesap ekstrenizi kontrol edin.",
		"A dues reminder has been sent. Please check your statement.")
	if link != "" {
		s += "\n" + openLink(link, pick(locale, "Ekstrem", "My statement"))
	}
	return s
}

// PollResultHTML is the tally of a closed, non-binding poll.
func PollResultHTML(locale, question string, yes, no, abstain, answered, total int64) string {
	return "<b>" + pick(locale, "Anket sonucu", "Poll result") + "</b>\n" + esc(question) + "\n\n" +
		fmt.Sprintf("%s: %d\n%s: %d\n%s: %d\n\n", pick(locale, "Evet", "Yes"), yes, pick(locale, "Hayır", "No"), no, pick(locale, "Çekimser", "Abstain"), abstain) +
		esc(fmt.Sprintf(pick(locale, "%d / %d bağımsız bölüm yanıtladı. Anket bağlayıcı değildir.", "%d of %d units answered. The poll is not binding."), answered, total))
}

// PollOptions are the answers of the mirrored poll, in the order the votes map back.
func PollOptions(locale string) []string {
	return []string{pick(locale, "Evet", "Yes"), pick(locale, "Hayır", "No"), pick(locale, "Çekimser", "Abstain")}
}
