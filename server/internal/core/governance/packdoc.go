package governance

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SetTemplates gives the service the resolver of pack document templates. Without
// one (or when no enabled pack has a template of a kind) the generic documents
// of this package are rendered.
func (s *Service) SetTemplates(r doctpl.Resolver) { s.templates = r }

// packModel renders a document with a pack template. ok is false when the
// organization has none for the kind and locale; a template that fails is
// logged and treated as absent: a broken template never blocks calling a
// meeting or writing the minutes.
func (s *Service) packModel(ctx context.Context, org uuid.UUID, kind, locale string, data map[string]any, qr []byte) (DocModel, bool) {
	if s.templates == nil {
		return DocModel{}, false
	}
	doc, ok, err := s.templates.Render(ctx, org, kind, locale, data)
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

// ---- pack-only documents

// PackDocument renders a document that only a pack provides: the per-owner call
// delivery checklist (delivery_checklist), the proxy form (proxy_form, blank, or
// filled for person) and the numbered karar defteri page draft
// (decision_book_draft). There is no generic equivalent: a 404 says no enabled
// pack offers the template. It needs meetings.manage.
func (s *Service) PackDocument(ctx context.Context, orgID, meetingID uuid.UUID, kind string, person *uuid.UUID) ([]byte, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return nil, "", err
	}
	switch kind {
	case doctpl.KindDeliveryChecklist, doctpl.KindProxyForm, doctpl.KindDecisionBookDraft:
	default:
		return nil, "", problem.NotFound("no such document")
	}
	var pdf []byte
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, _, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, false)
		if err != nil {
			return err
		}
		si, err := loadSiteInfo(ctx, tx, orgID, m.SiteID)
		if err != nil {
			return err
		}
		data := map[string]any{"site_name": si.Name, "kind": m.Kind, "locale": si.Locale, "scheduled_at": fmtTime(m.ScheduledAt, si.Loc, si.Locale),
			"second_call": fmtTime(m.SecondCallAt, si.Loc, si.Locale), "location": m.Location}
		switch kind {
		case doctpl.KindDeliveryChecklist:
			if len(m.EligibilitySnapshot) == 0 {
				return problem.Conflict("the meeting has not been called, so there is nothing to deliver")
			}
			snap, err := loadSnapshot(m.EligibilitySnapshot)
			if err != nil {
				return err
			}
			notices := map[uuid.UUID]Notice{}
			rows, err := tx.Query(ctx, noticeSelect+` WHERE n.org_id = $1 AND n.meeting_id = $2 AND n.phase = 'call'`, orgID, meetingID)
			if err != nil {
				return err
			}
			for rows.Next() {
				n, err := scanNotice(rows)
				if err != nil {
					rows.Close()
					return err
				}
				notices[n.PersonID] = n
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			set := m.rules()
			owners := make([]any, 0, len(snap.Owners))
			for i, o := range snap.Owners {
				var units []string
				for _, u := range o.Units {
					units = append(units, u.DoorNumber)
				}
				row := map[string]any{"no": i + 1, "name": o.DisplayName, "units": strings.Join(units, ", ")}
				if n, ok := notices[o.PersonID]; ok && n.SentAt != nil {
					row["sent_at"] = fmtTime(*n.SentAt, si.Loc, si.Locale)
					if n.Method != nil {
						row["method"] = *n.Method
					}
					if n.TrackingNo != nil {
						row["proof"] = *n.TrackingNo
					}
				}
				owners = append(owners, row)
			}
			data["owners"], data["min_notice_days"] = owners, set.CallMinDays
		case doctpl.KindProxyForm:
			if person != nil && len(m.EligibilitySnapshot) > 0 {
				if snap, err := loadSnapshot(m.EligibilitySnapshot); err == nil {
					if o, ok := snap.Owner(*person); ok {
						var units []string
						for _, u := range o.Units {
							units = append(units, u.DoorNumber)
						}
						data["owner_name"], data["units"] = o.DisplayName, strings.Join(units, ", ")
					}
				}
			}
		case doctpl.KindDecisionBookDraft:
			md, err := s.minutesData(ctx, tx, m)
			if err != nil {
				return err
			}
			decs := make([]any, 0, len(md.Items))
			for _, it := range md.Items {
				if dec := decisionData(it.Decision); dec != nil {
					decs = append(decs, dec)
				}
			}
			data["decisions"] = decs
		}
		model, ok := s.packModel(ctx, orgID, kind, si.Locale, data, nil)
		if !ok {
			return problem.NotFound("no template for this document is available for the site")
		}
		pdf, err = model.PDF()
		return err
	})
	return pdf, strings.ReplaceAll(kind, "_", "-") + ".pdf", dbErr(err, "document")
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
