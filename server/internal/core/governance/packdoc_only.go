package governance

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ---- pack-only documents

// packOnlyKinds are the documents that only a pack provides and that have no
// generic equivalent in the core.
var packOnlyKinds = []string{doctpl.KindDeliveryChecklist, doctpl.KindProxyForm, doctpl.KindDecisionBookDraft}

func isPackOnlyKind(kind string) bool {
	for _, k := range packOnlyKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// packDocFileName is the file name of a pack-only document ("proxy-form.pdf").
func packDocFileName(kind string) string { return strings.ReplaceAll(kind, "_", "-") + ".pdf" }

// PackDocument renders a document that only a pack provides: the per-owner call
// delivery checklist (delivery_checklist), the proxy form (proxy_form, blank, or
// filled for person) and the numbered karar defteri page draft
// (decision_book_draft). There is no generic equivalent: a 404 says no enabled
// pack offers the template. It renders on demand, with the delivery records and
// decisions as they are now; the blank checklist, the blank proxy form and the
// draft are also filed with the meeting's documents when it is called and when
// its minutes are generated (filePackDocuments). It needs meetings.manage.
func (s *Service) PackDocument(ctx context.Context, orgID, meetingID uuid.UUID, kind string, person *uuid.UUID) ([]byte, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return nil, "", err
	}
	if !isPackOnlyKind(kind) {
		return nil, "", problem.NotFound("no such document")
	}
	var pdf []byte
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, _, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, false)
		if err != nil {
			return err
		}
		data, locale, err := s.packDocData(ctx, tx, orgID, m, kind, person)
		if err != nil {
			return err
		}
		model, ok := s.packModel(ctx, orgID, kind, locale, data, nil)
		if !ok {
			return problem.NotFound("no template for this document is available for the site")
		}
		pdf, err = model.PDF()
		return err
	})
	return pdf, packDocFileName(kind), dbErr(err, "document")
}

// filePackDocuments files the pack-only documents of kinds with the meeting's
// documents, next to the call letter and the minutes, as the generic documents
// are: the blank delivery checklist and proxy form when the meeting is called,
// the decision book draft when its minutes are generated. A kind no enabled pack
// has a template for is skipped, and so is a template that fails or cannot be
// typeset (logged): a pack never blocks a call or the minutes. m must be the
// meeting as stored after the step (with its eligibility snapshot).
func (s *Service) filePackDocuments(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, m Meeting, kinds ...string) error {
	if s.docResolver == nil {
		return nil
	}
	for _, kind := range kinds {
		data, locale, err := s.packDocData(ctx, tx, orgID, m, kind, nil)
		if err != nil {
			return err
		}
		model, ok := s.packModel(ctx, orgID, kind, locale, data, nil)
		if !ok {
			continue
		}
		pdf, err := model.PDF()
		if err != nil {
			s.log.WarnContext(ctx, "pack document could not be typeset; it is not filed", "kind", kind, "error", err)
			continue
		}
		if _, err := s.filer.File(ctx, tx, orgID, Document{Kind: kind, FileName: packDocFileName(kind), MIME: "application/pdf", Content: pdf, SiteID: m.SiteID, MeetingID: m.ID}); err != nil {
			return err
		}
	}
	return nil
}

// packDocData builds the data a pack-only template receives and the locale of
// the site. person fills the proxy form for that owner.
func (s *Service) packDocData(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, m Meeting, kind string, person *uuid.UUID) (map[string]any, string, error) {
	si, err := loadSiteInfo(ctx, tx, orgID, m.SiteID)
	if err != nil {
		return nil, "", err
	}
	data := map[string]any{"site_name": si.Name, "kind": m.Kind, "locale": si.Locale, "scheduled_at": fmtTime(m.ScheduledAt, si.Loc, si.Locale),
		"second_call": fmtTime(m.SecondCallAt, si.Loc, si.Locale), "location": m.Location}
	switch kind {
	case doctpl.KindDeliveryChecklist:
		if len(m.EligibilitySnapshot) == 0 {
			return nil, "", problem.Conflict("the meeting has not been called, so there is nothing to deliver")
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return nil, "", err
		}
		notices := map[uuid.UUID]Notice{}
		rows, err := tx.Query(ctx, noticeSelect+` WHERE n.org_id = $1 AND n.meeting_id = $2 AND n.phase = 'call'`, orgID, m.ID)
		if err != nil {
			return nil, "", err
		}
		for rows.Next() {
			n, err := scanNotice(rows)
			if err != nil {
				rows.Close()
				return nil, "", err
			}
			notices[n.PersonID] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, "", err
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
			return nil, "", err
		}
		decs := make([]any, 0, len(md.Items))
		for _, it := range md.Items {
			if dec := decisionData(it.Decision); dec != nil {
				decs = append(decs, dec)
			}
		}
		data["decisions"] = decs
	}
	return data, si.Locale, nil
}
