package governance

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
)

// SetTemplates gives the service the resolver of pack document templates. Without
// one (or when no enabled pack has a template of a kind) the generic documents
// of this package are rendered.
func (s *Service) SetTemplates(r doctpl.Resolver) { s.docResolver = r }

// packModel renders a document with a pack template. ok is false when the
// organization has none for the kind and locale; a template that fails is
// logged and treated as absent: a broken template never blocks calling a
// meeting or writing the minutes.
func (s *Service) packModel(ctx context.Context, org uuid.UUID, kind, locale string, data map[string]any, qr []byte) (DocModel, bool) {
	if s.docResolver == nil {
		return DocModel{}, false
	}
	doc, ok, err := s.docResolver.Render(ctx, org, kind, locale, data)
	if err != nil {
		s.log.WarnContext(ctx, "pack document template failed; the generic document is used", "kind", kind, "error", err)
		return DocModel{}, false
	}
	if !ok || !doc.IsMarkdown() {
		return DocModel{}, false
	}
	return ModelFromMarkdown(string(doc.Body), qr), true
}

// callLetterModel is the call letter: a pack's template, else the generic one.
func (s *Service) callLetterModel(ctx context.Context, org uuid.UUID, d CallLetterData) DocModel {
	if m, ok := s.packModel(ctx, org, doctpl.KindCallLetter, d.Locale, d.data(), d.QRPNG); ok {
		return m
	}
	return CallLetter(d)
}

func (s *Service) attendanceModel(ctx context.Context, org uuid.UUID, d AttendanceSheetData) DocModel {
	if m, ok := s.packModel(ctx, org, doctpl.KindAttendanceList, d.Locale, d.data(), nil); ok {
		return m
	}
	return AttendanceSheet(d)
}

func (s *Service) minutesModel(ctx context.Context, org uuid.UUID, d MinutesData) DocModel {
	if m, ok := s.packModel(ctx, org, doctpl.KindMinutes, d.Locale, d.data(), nil); ok {
		return m
	}
	return Minutes(d)
}

// ---- the data of the documents, as pack templates receive it (README of the pack)

func (d CallLetterData) data() map[string]any {
	agenda := make([]any, 0, len(d.Agenda))
	for _, a := range d.Agenda {
		agenda = append(agenda, map[string]any{"position": a.Position, "title": a.Title, "description": a.Description, "will_be_voted": a.WillBeVoted})
	}
	return map[string]any{"site_name": d.SiteName, "kind": d.Kind, "title": d.Title, "locale": d.Locale,
		"scheduled_at": fmtTime(d.ScheduledAt, d.Loc, d.Locale), "second_call": fmtTime(d.SecondCall, d.Loc, d.Locale), "location": d.Location,
		"agenda": agenda, "recipient": d.Recipient, "qr_token": d.QRToken, "issued_by": d.Instance, "issued_at": fmtTime(d.IssuedAt, d.Loc, d.Locale)}
}

func (d AttendanceSheetData) data() map[string]any {
	rows := make([]any, 0, len(d.Rows))
	for i, r := range d.Rows {
		rows = append(rows, map[string]any{"no": i + 1, "name": r.Name, "units": strings.Join(r.Units, ", "), "proxy_for": r.ProxyFor, "votes": r.Votes, "share": fmtShare(r.Share)})
	}
	return map[string]any{"site_name": d.SiteName, "locale": d.Locale, "scheduled_at": fmtTime(d.ScheduledAt, d.Loc, d.Locale), "location": d.Location, "call": d.Call,
		"rows": rows, "eligible_votes": d.Eligible.Votes, "eligible_share": fmtShare(d.Eligible.Share), "present_votes": d.Present.Votes, "present_share": fmtShare(d.Present.Share),
		"quorum_reached": d.Quorum.Reached, "no_quorum_required": d.Quorum.NoQuorumSecondCall}
}

func decisionData(dec *MinutesDecision) map[string]any {
	if dec == nil {
		return nil
	}
	dissents := make([]any, 0, len(dec.Dissents))
	for _, ds := range dec.Dissents {
		dissents = append(dissents, map[string]any{"name": ds.Name, "text": ds.Text})
	}
	return map[string]any{"year": dec.Year, "seq": dec.Seq, "text": dec.Text, "outcome": dec.Outcome, "has_votes": dec.HasVotes, "yes": dec.Yes, "no": dec.No, "abstain": dec.Abstain, "dissents": dissents}
}

func (d MinutesData) data() map[string]any {
	items := make([]any, 0, len(d.Items))
	for _, it := range d.Items {
		m := map[string]any{"position": it.Position, "title": it.Title, "will_be_voted": it.WillBeVoted}
		if dec := decisionData(it.Decision); dec != nil {
			m["decision"] = dec
		}
		items = append(items, m)
	}
	return map[string]any{"site_name": d.SiteName, "kind": d.Kind, "locale": d.Locale, "scheduled_at": fmtTime(d.ScheduledAt, d.Loc, d.Locale), "second_call": fmtTime(d.SecondCall, d.Loc, d.Locale),
		"location": d.Location, "call": d.Call, "attended": d.Attended, "eligible_votes": d.Eligible.Votes, "eligible_share": fmtShare(d.Eligible.Share),
		"present_votes": d.Present.Votes, "present_share": fmtShare(d.Present.Share), "quorum_reached": d.Quorum.Reached, "items": items}
}

// ---- Markdown to DocModel

var (
	kvLine   = regexp.MustCompile(`^\*\*(.+?):\*\*\s*(.*)$`)
	signLine = regexp.MustCompile(`^\[sign:\s*(.*)\]$`)
	qrLine   = regexp.MustCompile(`^\[qr:\s*(.*)\]$`)
)

// ModelFromMarkdown typesets the small Markdown subset a pack template returns
// (sdk.Document): "# " title, leading "> " subtitle lines, "## " headings,
// "- " items, "**Label:** value", pipe tables, "[sign: X]", "[qr: caption]"
// (drawn with qrPNG; plain text when there is none) and "---". Consecutive text
// lines form one paragraph; ** markers are dropped.
func ModelFromMarkdown(md string, qrPNG []byte) DocModel {
	var m DocModel
	plain := func(s string) string { return strings.ReplaceAll(s, "**", "") }
	var para []string
	flush := func() {
		if len(para) > 0 {
			m.Body = append(m.Body, DocBlock{Kind: "p", Text: plain(strings.Join(para, " "))})
			para = nil
		}
	}
	var tbl *DocBlock
	endTable := func() {
		if tbl != nil {
			m.Body = append(m.Body, *tbl)
			tbl = nil
		}
	}
	titled := false
	lead := true // subtitle lines directly under the title
	for _, raw := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(line, "|") {
			flush()
			cells := splitRow(line)
			switch {
			case tbl == nil:
				tbl = &DocBlock{Kind: "table", Header: cells}
			case isSeparator(cells):
			default:
				for len(cells) < len(tbl.Header) {
					cells = append(cells, "")
				}
				tbl.Rows = append(tbl.Rows, cells[:len(tbl.Header)])
			}
			continue
		}
		endTable()
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "# ") && !titled:
			m.Title, titled = plain(strings.TrimPrefix(line, "# ")), true
		case strings.HasPrefix(line, "> ") && lead:
			m.Lines = append(m.Lines, plain(strings.TrimPrefix(line, "> ")))
		case strings.HasPrefix(line, "## "):
			flush()
			lead = false
			m.Body = append(m.Body, DocBlock{Kind: "h", Text: plain(strings.TrimPrefix(line, "## "))})
		case strings.HasPrefix(line, "- "):
			flush()
			lead = false
			m.Body = append(m.Body, DocBlock{Kind: "li", Text: plain(strings.TrimPrefix(line, "- "))})
		case line == "---":
			flush()
			m.Body = append(m.Body, DocBlock{Kind: "space"})
		case signLine.MatchString(line):
			flush()
			lead = false
			m.Body = append(m.Body, DocBlock{Kind: "sign", Text: plain(signLine.FindStringSubmatch(line)[1])})
		case qrLine.MatchString(line):
			flush()
			lead = false
			caption := plain(qrLine.FindStringSubmatch(line)[1])
			if len(qrPNG) > 0 {
				m.Body = append(m.Body, DocBlock{Kind: "qr", Text: caption, PNG: qrPNG})
			} else {
				m.Body = append(m.Body, DocBlock{Kind: "p", Text: caption})
			}
		case kvLine.MatchString(line):
			flush()
			lead = false
			sm := kvLine.FindStringSubmatch(line)
			m.Body = append(m.Body, DocBlock{Kind: "kv", Label: plain(sm[1]), Text: plain(sm[2])})
		default:
			lead = false
			para = append(para, strings.TrimSpace(line))
		}
	}
	flush()
	endTable()
	if m.Title == "" {
		m.Title = "-"
	}
	return m
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.ReplaceAll(parts[i], "**", ""))
	}
	return parts
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

var _ = fmt.Sprint
