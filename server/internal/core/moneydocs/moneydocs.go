// Package moneydocs makes the documents of the money module: payment receipts, the move-out
// closing statement and debt certificate, and sent statements. Each is a PDF written with
// platform/pdf and filed through the file module as a generated file.
//
// With a documents.Filer set (SetFiler) every document is filed into the unit's folder of the
// site's documents (a private folder the unit's people read) through documents.FileGenerated, once
// per source record; without one they are files of the scope payment:<id>, move_out:<id> and
// statement:<id>.
package moneydocs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/packdocs"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/pdf"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Scope types of the generated files.
const (
	ScopePayment   = "payment"
	ScopeMoveOut   = "move_out"
	ScopeStatement = "statement"
)

// Service makes and files the money documents.
type Service struct {
	db       ledger.TxRunner
	Files    *files.Service
	Payments *payments.Service
	Reports  *reports.Service
	Notifier ledger.Notifier
	// Docs files the generated documents into the unit's folder (nil: files of their own scope).
	Docs documents.Filer
	// Templates renders documents with a template of an enabled pack when there is one (nil: generic documents).
	PackDocs doctpl.Resolver
	Now      func() time.Time
	Log      *slog.Logger
}

// NewService returns a Service. notifier may be nil (nothing is announced).
func NewService(db ledger.TxRunner, fs *files.Service, pay *payments.Service, rep *reports.Service, notifier ledger.Notifier, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, Files: fs, Payments: pay, Reports: rep, Notifier: notifier, Now: time.Now, Log: log}
}

// SetFiler makes the service file its documents into the unit's folder of the documents module.
func (s *Service) SetFiler(f documents.Filer) { s.Docs = f }

// fileDocument files content as a generated document of the unit (site folder systemKey when the
// debtor is a person with no unit), once per (genType, genID), and returns its file.
func (s *Service) fileDocument(ctx context.Context, orgID, siteID uuid.UUID, unit *uuid.UUID, recipient *uuid.UUID, title, name, genType string, genID uuid.UUID, content []byte) (files.File, error) {
	var out files.File
	err := s.run(ctx, orgID, "document", func(tx pgx.Tx) error {
		d, err := s.Docs.FileGenerated(ctx, tx, documents.GeneratedParams{
			OrgID: orgID, SiteID: siteID, Title: title, FileName: name, Content: bytes.NewReader(content),
			UnitID: unit, SystemKey: "money", FolderName: "Money", FolderVisibility: documents.Visibility{Level: documents.LevelBoard},
			GeneratedType: genType, GeneratedID: genID, RecipientPersonID: recipient,
		})
		if err != nil {
			return err
		}
		out = files.File{ID: d.FileID}
		return nil
	})
	return out, err
}

func (s *Service) run(ctx context.Context, orgID uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.db.InTenantTx(ctx, orgID, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return ledger.DBProblem(err, what)
}

// amount renders money for a document in the number format of the organization's
// locale (Turkish: ₺1.234,50, anything else: the English format, $1,234.50) with the
// sign of the currency.
func amount(locale string, m money.Money) string {
	tag := "en-US"
	if l := strings.ToLower(locale); l == "tr" || strings.HasPrefix(l, "tr-") || strings.HasPrefix(l, "tr_") {
		tag = "tr-TR"
	}
	s, err := m.Format(tag)
	if err != nil {
		return m.String()
	}
	return s
}

// ReceiptPDF writes the receipt (makbuz) of a confirmed payment. The caller has read access to the payment.
func (s *Service) ReceiptPDF(ctx context.Context, orgID, paymentID uuid.UUID) ([]byte, string, error) {
	return s.receiptPDF(ctx, orgID, paymentID, "")
}

// receiptPDF is ReceiptPDF and, with creditNo set, the credit note of a voided or refunded payment
// (the same data and template kind, flagged credit_note); it returns the number of the document.
func (s *Service) receiptPDF(ctx context.Context, orgID, paymentID uuid.UUID, creditNo string) ([]byte, string, error) {
	p, err := s.Payments.GetForReceipt(ctx, orgID, paymentID)
	if err != nil {
		return nil, "", err
	}
	credit := creditNo != ""
	if p.ReceiptNo == nil || (!credit && p.Status != "confirmed") || (credit && p.Status != "voided" && p.Status != "refunded") {
		return nil, "", problem.Conflict("only a confirmed payment has a receipt")
	}
	var site tenancy.Site
	var door, who, balanceLine, locale string
	type line struct {
		desc, amt string
		advance   bool
	}
	var lines []line
	err = s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		var err error
		if site, err = tenancy.LoadSite(ctx, tx, orgID, p.SiteID, false, true); err != nil {
			return err
		}
		locale = packdocs.OrgLocale(ctx, tx, orgID)
		d := billing.Debtor{PersonID: p.PersonID}
		if p.UnitID != nil && p.DebtorKind != nil && *p.DebtorKind != "person" {
			_ = tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id=$1 AND id=$2`, orgID, *p.UnitID).Scan(&door)
			d = billing.Debtor{UnitID: p.UnitID, Kind: *p.DebtorKind}
		}
		if p.PersonID != nil {
			_ = tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id=$1 AND id=$2`, orgID, *p.PersonID).Scan(&who)
		} else if p.PayerPersonID != nil {
			_ = tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id=$1 AND id=$2`, orgID, *p.PayerPersonID).Scan(&who)
		}
		for _, a := range p.Allocations {
			desc := "Credit"
			if a.ChargeID != nil {
				_ = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(description, ''), charge_type) || CASE WHEN period <> '' THEN ' ' || period ELSE '' END FROM charge WHERE org_id=$1 AND id=$2`, orgID, *a.ChargeID).Scan(&desc)
			}
			lines = append(lines, line{desc, amount(locale, a.Amount), a.ChargeID == nil})
		}
		if p.Unallocated.Amount > 0 {
			lines = append(lines, line{"Advance payment (credit)", amount(locale, p.Unallocated), true})
		}
		charges, err := billing.LoadCharges(ctx, tx, orgID, p.SiteID, d)
		if err != nil {
			return err
		}
		pr, fee := billing.OpenTotal(charges)
		balanceLine = amount(locale, money.New(pr+fee, p.Net.Currency))
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	// a pack's template (the Turkish aidat makbuzu) replaces the generic receipt
	packLines := make([]any, 0, len(lines))
	for _, l := range lines {
		desc := l.desc
		if l.advance {
			desc = ""
		}
		packLines = append(packLines, map[string]any{"desc": desc, "amount": l.amt, "advance": l.advance})
	}
	no := *p.ReceiptNo
	date := p.ReceivedOn
	data := map[string]any{
		"site_name": site.Name, "receipt_no": no, "date": date, "payer": who, "unit": door, "method": p.Method, "kind": deref(p.DebtorKind),
		"reference": deref(p.Reference), "lines": packLines, "total": amount(locale, p.Net), "balance": balanceLine}
	if credit {
		no, date = creditNo, s.Now().UTC().Format(time.DateOnly)
		data["credit_note"], data["credit_note_no"], data["date"] = true, creditNo, date
	}
	if b, ok := packdocs.Render(ctx, s.PackDocs, s.Log, orgID, doctpl.KindReceipt, locale, data); ok {
		return b, no, nil
	}
	title, totalLabel := "Makbuz / Receipt  ", "Total received / Toplam tahsilat"
	if credit {
		title, totalLabel = "Iade makbuzu / Credit note  ", "Total refunded / Toplam iade"
	}
	doc := pdf.New(strings.TrimSpace(title) + " " + no)
	doc.Line(site.Name, 14, true)
	doc.Line(title+no, 12, true)
	if credit {
		doc.Line("Receipt / Makbuz  "+*p.ReceiptNo, 10, false)
	}
	doc.Skip(6)
	row := func(k, v string) {
		doc.Text(pdf.MarginX, k, 10, true)
		doc.Text(pdf.MarginX+130, v, 10, false)
		doc.Skip(15)
	}
	row("Date / Tarih", date)
	if door != "" {
		row("Unit / Daire", door+" ("+strings.ToLower(deref(p.DebtorKind))+")")
	}
	if who != "" {
		row("Name / Ad", who)
	}
	row("Method / Yontem", map[string]string{"cash": "Cash / Nakit", "bank_transfer": "Bank transfer / Havale"}[p.Method])
	if p.Reference != nil && *p.Reference != "" {
		row("Reference", *p.Reference)
	}
	doc.Skip(4)
	doc.Rule()
	doc.Skip(6)
	doc.Text(pdf.MarginX, "Applied to / Karsiligi", 10, true)
	doc.Skip(15)
	for _, l := range lines {
		doc.Text(pdf.MarginX+10, l.desc, 10, false)
		doc.Right(pdf.PageW-pdf.MarginX, l.amt, 10, false)
		doc.Skip(14)
	}
	doc.Rule()
	doc.Skip(6)
	doc.Text(pdf.MarginX, totalLabel, 11, true)
	doc.Right(pdf.PageW-pdf.MarginX, amount(locale, p.Net), 11, true)
	doc.Skip(16)
	doc.Text(pdf.MarginX, "Balance still due / Kalan borc", 10, false)
	doc.Right(pdf.PageW-pdf.MarginX, balanceLine, 10, false)
	return doc.Bytes(), no, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// FileReceipt writes the receipt of a confirmed payment and files it as a generated file of the scope
// payment:<id> (once: a second call returns the filed one). Failing to file never undoes a payment: callers log the error.
func (s *Service) FileReceipt(ctx context.Context, orgID, paymentID uuid.UUID) (files.File, error) {
	if s.Files == nil {
		return files.File{}, errors.New("moneydocs: no file service")
	}
	if s.Docs != nil {
		b, no, err := s.ReceiptPDF(ctx, orgID, paymentID)
		if err != nil {
			return files.File{}, err
		}
		p, err := s.Payments.GetForReceipt(ctx, orgID, paymentID)
		if err != nil {
			return files.File{}, err
		}
		return s.fileDocument(ctx, orgID, p.SiteID, p.UnitID, p.PersonID, "Receipt "+no, "receipt-"+no+".pdf", "payment_receipt", paymentID, b)
	}
	var existing files.File
	found := false
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		var fid uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM file WHERE org_id=$1 AND scope_type=$2 AND scope_id=$3 AND status <> 'deleted' ORDER BY created_at LIMIT 1`, orgID, ScopePayment, paymentID).Scan(&fid)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found, existing = true, files.File{ID: fid}
		return nil
	})
	if err != nil {
		return files.File{}, err
	}
	if found {
		return existing, nil
	}
	b, no, err := s.ReceiptPDF(ctx, orgID, paymentID)
	if err != nil {
		return files.File{}, err
	}
	p, err := s.Payments.GetForReceipt(ctx, orgID, paymentID)
	if err != nil {
		return files.File{}, err
	}
	site := p.SiteID
	return s.Files.UploadGenerated(ctx, files.UploadParams{OrgID: orgID, SiteID: &site, Name: "receipt-" + no + ".pdf", Content: bytes.NewReader(b), Scope: &files.ScopeRef{Type: ScopePayment, ID: paymentID}})
}

// SendReceipt announces that the receipt of a payment is to be sent on the channels (payments.record).
func (s *Service) SendReceipt(ctx context.Context, orgID, paymentID uuid.UUID, channels []string) error {
	p, err := s.Payments.GetForReceipt(ctx, orgID, paymentID)
	if err != nil {
		return err
	}
	if p.Status != "confirmed" {
		return problem.Conflict("only a confirmed payment has a receipt")
	}
	if len(channels) == 0 {
		return problem.Validation(problem.Field{Field: "channels", Code: "required"})
	}
	if s.Files != nil {
		if _, err := s.FileReceipt(ctx, orgID, paymentID); err != nil {
			s.Log.Warn("filing the receipt", "payment_id", paymentID, "error", err)
		}
	}
	if s.Notifier == nil {
		return nil
	}
	return s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		for _, ch := range channels {
			payload, _ := json.Marshal(map[string]any{"payment_id": paymentID, "channel": ch})
			if err := s.Notifier.Enqueue(ctx, tx, ledger.Notification{Type: ledger.NotifyReceiptSend, OrgID: orgID, SiteID: p.SiteID, Payload: payload}); err != nil {
				return err
			}
		}
		return nil
	})
}

// StatementSent is the answer of SendStatement.
type StatementSent struct {
	DocumentID     uuid.UUID `json:"document_id"`
	FileID         uuid.UUID `json:"file_id"`
	RecipientCount int       `json:"recipient_count"`
	SMSCount       int       `json:"sms_count"`
}

// SendStatement files the statement of a debtor as a PDF and announces it to the debtor's person on the
// channels (statements.read): one money notification per channel carries the file. The recipients are the
// person of the debtor (the carried person, or the current occupants of the unit side with a contact).
func (s *Service) SendStatement(ctx context.Context, orgID uuid.UUID, d billing.Debtor, from, to string, channels []string) (StatementSent, error) {
	var out StatementSent
	if err := authz.RequireAnywhere(ctx, ledger.PermStatementsRead); err != nil {
		return out, err
	}
	var siteID uuid.UUID
	if err := s.run(ctx, orgID, "debtor", func(tx pgx.Tx) error {
		if d.IsPerson() {
			return tx.QueryRow(ctx, `SELECT site_id FROM charge WHERE org_id=$1 AND debtor_kind='person' AND debtor_person_id=$2 ORDER BY created_at DESC LIMIT 1`, orgID, *d.PersonID).Scan(&siteID)
		}
		if d.UnitID == nil {
			return problem.Validation(problem.Field{Field: "debtor", Code: "invalid_value"})
		}
		return tx.QueryRow(ctx, `SELECT site_id FROM unit WHERE org_id=$1 AND id=$2`, orgID, *d.UnitID).Scan(&siteID)
	}); err != nil {
		return out, err
	}
	if len(channels) == 0 {
		return out, problem.Validation(problem.Field{Field: "channels", Code: "required"})
	}
	if s.Files == nil {
		return out, problem.Internal(errors.New("moneydocs: no file service"))
	}
	st, err := s.Reports.DebtorStatement(ctx, orgID, siteID, d, from, to)
	if err != nil {
		return out, err
	}
	doc := id.New()
	var f files.File
	if s.Docs != nil {
		f, err = s.fileDocument(ctx, orgID, siteID, d.UnitID, d.PersonID, "Statement "+st.AsOf, "statement-"+st.AsOf+".pdf", "statement", doc, s.Reports.StatementPDF(ctx, orgID, st, "statement"))
	} else {
		f, err = s.Files.UploadGenerated(ctx, files.UploadParams{OrgID: orgID, SiteID: &siteID, Name: "statement-" + st.AsOf + ".pdf", Content: bytes.NewReader(s.Reports.StatementPDF(ctx, orgID, st, "statement")),
			Scope: &files.ScopeRef{Type: ScopeStatement, ID: doc}})
	}
	if err != nil {
		return out, err
	}
	out.DocumentID, out.FileID = doc, f.ID
	err = s.run(ctx, orgID, "statement", func(tx pgx.Tx) error {
		recips, phones, err := recipients(ctx, tx, orgID, siteID, d)
		if err != nil {
			return err
		}
		out.RecipientCount = recips
		for _, ch := range channels {
			if ch == "sms" {
				out.SMSCount = phones
			}
			if s.Notifier == nil {
				continue
			}
			payload, _ := json.Marshal(map[string]any{"debtor": d, "from": from, "to": to, "channel": ch, "file_id": f.ID})
			if err := s.Notifier.Enqueue(ctx, tx, ledger.Notification{Type: ledger.NotifyStatementSend, OrgID: orgID, SiteID: siteID, Payload: payload}); err != nil {
				return err
			}
		}
		return ledger.Audit(ctx, tx, orgID, siteID, "statement.send", "charge", doc, nil, map[string]any{"channels": channels, "recipients": recips})
	})
	return out, err
}

// recipients counts the persons a statement of the debtor goes to and how many of them have a phone number.
func recipients(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d billing.Debtor) (people, phones int, err error) {
	var rows pgx.Rows
	if d.IsPerson() {
		rows, err = tx.Query(ctx, `SELECT COALESCE(contact->>'phone', '') FROM person WHERE org_id=$1 AND id=$2`, orgID, *d.PersonID)
	} else {
		rows, err = tx.Query(ctx, `SELECT DISTINCT p.id, COALESCE(p.contact->>'phone', '') FROM occupancy o JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
			WHERE o.org_id=$1 AND o.site_id=$2 AND o.unit_id=$3 AND o.kind=$4 AND o.valid_from <= current_date AND (o.valid_to IS NULL OR o.valid_to > current_date)`, orgID, siteID, *d.UnitID, d.Kind)
	}
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var phone string
		if d.IsPerson() {
			err = rows.Scan(&phone)
		} else {
			var pid uuid.UUID
			err = rows.Scan(&pid, &phone)
		}
		if err != nil {
			return 0, 0, err
		}
		people++
		if phone != "" {
			phones++
		}
	}
	return people, phones, rows.Err()
}

// closingJob is a move-out whose documents are still to be made.
type closingJob struct {
	id, site, unit, person  uuid.UUID
	side, asOf              string
	carried                 int64
	currency, openCharges   string
	needStatement, needCert bool
}

// FillMoveOutDocuments makes the closing statement and the debt certificate of every move-out of
// the organization that is still waiting for them and sets the file ids on the move-out (the hourly job).
// It returns the number of move-outs completed.
func (s *Service) FillMoveOutDocuments(ctx context.Context, orgID uuid.UUID) (int, error) {
	var jobs []closingJob
	err := s.run(ctx, orgID, "move-out", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT m.id, m.site_id, m.unit_id, m.person_id, COALESCE(o.kind, 'owner'), m.as_of::text, COALESCE(m.carried_amount_minor, 0), COALESCE(m.carried_currency, 'TRY'), m.open_charges,
				m.closing_statement_status = 'pending', m.certificate_status = 'pending'
			FROM move_out m LEFT JOIN occupancy o ON o.org_id = m.org_id AND o.id = m.occupancy_id
			WHERE m.org_id = $1 AND (m.closing_statement_status = 'pending' OR m.certificate_status = 'pending') ORDER BY m.id LIMIT 50`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j closingJob
			if err := rows.Scan(&j.id, &j.site, &j.unit, &j.person, &j.side, &j.asOf, &j.carried, &j.currency, &j.openCharges, &j.needStatement, &j.needCert); err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	done := 0
	for _, j := range jobs {
		if err := s.fillOne(ctx, orgID, j); err != nil {
			s.Log.Error("move-out documents", "move_out_id", j.id, "error", err)
			continue
		}
		done++
	}
	return done, nil
}

func (s *Service) fillOne(ctx context.Context, orgID uuid.UUID, j closingJob) error {
	d := billing.Debtor{UnitID: &j.unit, Kind: j.side}
	var siteName, door, who, locale, siteTZ string
	err := s.run(ctx, orgID, "move-out", func(tx pgx.Tx) error {
		site, err := tenancy.LoadSite(ctx, tx, orgID, j.site, false, false)
		if err != nil {
			return err
		}
		siteName, siteTZ, locale = site.Name, site.Timezone, packdocs.OrgLocale(ctx, tx, orgID)
		_ = tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id=$1 AND id=$2`, orgID, j.unit).Scan(&door)
		_ = tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id=$1 AND id=$2`, orgID, j.person).Scan(&who)
		return nil
	})
	if err != nil {
		return err
	}
	var stFile, certFile *uuid.UUID
	site := j.site
	if j.needStatement {
		st, err := s.Reports.DebtorStatement(ctx, orgID, j.site, d, "", j.asOf)
		if err != nil {
			return err
		}
		var f files.File
		if s.Docs != nil {
			f, err = s.fileDocument(ctx, orgID, site, &j.unit, &j.person, "Closing statement "+j.asOf, "closing-statement-"+j.asOf+".pdf", "move_out_statement", j.id, s.Reports.StatementPDF(ctx, orgID, st, "closing_statement"))
		} else {
			f, err = s.Files.UploadGenerated(ctx, files.UploadParams{OrgID: orgID, SiteID: &site, Name: "closing-statement-" + j.asOf + ".pdf", Content: bytes.NewReader(s.Reports.StatementPDF(ctx, orgID, st, "closing_statement")),
				Scope: &files.ScopeRef{Type: ScopeMoveOut, ID: j.id}})
		}
		if err != nil {
			return err
		}
		stFile = &f.ID
	}
	if j.needCert {
		cur := money.Code(j.currency)
		certPDF, packed := packdocs.Render(ctx, s.PackDocs, s.Log, orgID, doctpl.KindDebtCertificate, locale, map[string]any{
			"site_name": siteName, "person": who, "unit": door + " (" + j.side + ")", "as_of": j.asOf, "move_out_date": j.asOf,
			"has_debt": j.carried != 0, "balance": amount(locale, money.New(j.carried, cur)), "open_charges": j.openCharges})
		doc := pdf.New("Debt status certificate")
		doc.Line(siteName, 14, true)
		doc.Line("Borc durum belgesi / Debt status certificate", 12, true)
		doc.Skip(10)
		for _, l := range []string{
			fmt.Sprintf("Unit / Daire: %s (%s)", door, j.side),
			"Name / Ad: " + who,
			"Date of move-out or transfer / Cikis veya devir tarihi: " + j.asOf,
		} {
			doc.Line(l, 10, false)
		}
		doc.Skip(6)
		switch {
		case j.carried == 0:
			doc.Line("No unpaid dues or late fees were open on this date. / Bu tarihte odenmemis aidat veya gecikme zammi yoktur.", 10, false)
		case j.openCharges == "write_off":
			doc.Line("Open amount written off by the management: "+amount(locale, money.New(j.carried, cur)), 10, false)
		case j.openCharges == "keep_on_unit":
			doc.Line("Open amounts stay on the unit.", 10, false)
		default:
			doc.Line("Open amount carried to the person: "+amount(locale, money.New(j.carried, cur)), 10, false)
		}
		doc.Skip(10)
		doc.Line("Issued on "+ledger.Today(siteTZ, s.Now())+" by the management system.", 8, false)
		certBody := doc.Bytes()
		if packed {
			certBody = certPDF
		}
		var f files.File
		var err error
		if s.Docs != nil {
			f, err = s.fileDocument(ctx, orgID, site, &j.unit, &j.person, "Debt status certificate "+j.asOf, "debt-certificate-"+j.asOf+".pdf", "move_out_certificate", j.id, certBody)
		} else {
			f, err = s.Files.UploadGenerated(ctx, files.UploadParams{OrgID: orgID, SiteID: &site, Name: "debt-certificate-" + j.asOf + ".pdf", Content: bytes.NewReader(certBody),
				Scope: &files.ScopeRef{Type: ScopeMoveOut, ID: j.id}})
		}
		if err != nil {
			return err
		}
		certFile = &f.ID
	}
	return s.run(ctx, orgID, "move-out", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE move_out SET
				closing_statement_file_id = COALESCE($3, closing_statement_file_id), closing_statement_status = CASE WHEN $3::uuid IS NOT NULL THEN 'ready' ELSE closing_statement_status END,
				certificate_file_id = COALESCE($4, certificate_file_id), certificate_status = CASE WHEN $4::uuid IS NOT NULL THEN 'ready' ELSE certificate_status END
			WHERE org_id=$1 AND id=$2`, orgID, j.id, stFile, certFile)
		return err
	})
}

// MoveOutFileResolver decides who may read the closing documents of a move-out: holders of statements.read at its site.
func MoveOutFileResolver() files.VisibilityResolver {
	return siteResolver(`SELECT site_id FROM move_out WHERE id = $1`)
}

// PaymentFileResolver decides who may read a receipt: holders of money.read or payments.record at the payment's site, or the resident
// of the unit side it was paid for.
func PaymentFileResolver() files.VisibilityResolver {
	return files.ResolverFuncs{Read: func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
		var site uuid.UUID
		var unit *uuid.UUID
		var kind *string
		if err := tx.QueryRow(ctx, `SELECT site_id, unit_id, debtor_kind FROM payment WHERE id = $1`, ref.ID).Scan(&site, &unit, &kind); err != nil {
			return false, nil
		}
		if ledger.HasSite(ctx, tx, ledger.PermMoneyRead, site) || ledger.HasSite(ctx, tx, ledger.PermPaymentsRecord, site) {
			return true, nil
		}
		if unit != nil && kind != nil && *kind != "person" {
			return billing.OwnSide(ctx, unit, *kind) == nil, nil
		}
		return false, nil
	}}
}

// StatementFileResolver decides who may read a sent statement: holders of statements.read at its site.
func StatementFileResolver() files.VisibilityResolver {
	return files.ResolverFuncs{Read: func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
		var site uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT site_id FROM file WHERE scope_type = $1 AND scope_id = $2 LIMIT 1`, ScopeStatement, ref.ID).Scan(&site); err != nil {
			return false, nil
		}
		return ledger.HasSite(ctx, tx, ledger.PermStatementsRead, site), nil
	}}
}

func siteResolver(query string) files.VisibilityResolver {
	return files.ResolverFuncs{Read: func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
		var site uuid.UUID
		if err := tx.QueryRow(ctx, query, ref.ID).Scan(&site); err != nil {
			return false, nil
		}
		return ledger.HasSite(ctx, tx, ledger.PermStatementsRead, site), nil
	}}
}

// FileCreditNote makes the credit note of a voided or refunded payment (a cash, bank or provider refund)
// and files it next to the receipt in the unit's documents, once: the number (CN-<year>-<n>, gap-free per
// site) is taken the first time and the filed document is linked as payment.credit_note_document_id.
// Failing to file never undoes the refund: callers log the error.
func (s *Service) FileCreditNote(ctx context.Context, orgID, paymentID uuid.UUID) (files.File, error) {
	if s.Files == nil || s.Docs == nil {
		return files.File{}, errors.New("moneydocs: no file or document service")
	}
	var no string
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		var status string
		var site uuid.UUID
		var cn *string
		if err := tx.QueryRow(ctx, `SELECT status, site_id, credit_note_no FROM payment WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, paymentID).Scan(&status, &site, &cn); err != nil {
			return err
		}
		if status != "voided" && status != "refunded" {
			return problem.Conflict("only a voided or refunded payment has a credit note")
		}
		if cn != nil {
			no = *cn
			return nil
		}
		year := s.Now().UTC().Year()
		n, err := ledger.NextNumber(ctx, tx, orgID, site, "credit_note", year)
		if err != nil {
			return err
		}
		no = fmt.Sprintf("CN-%d-%06d", year, n)
		_, err = tx.Exec(ctx, `UPDATE payment SET credit_note_no=$3 WHERE org_id=$1 AND id=$2`, orgID, paymentID, no)
		return err
	})
	if err != nil {
		return files.File{}, err
	}
	b, _, err := s.receiptPDF(ctx, orgID, paymentID, no)
	if err != nil {
		return files.File{}, err
	}
	p, err := s.Payments.GetForReceipt(ctx, orgID, paymentID)
	if err != nil {
		return files.File{}, err
	}
	var out files.File
	err = s.run(ctx, orgID, "document", func(tx pgx.Tx) error {
		d, err := s.Docs.FileGenerated(ctx, tx, documents.GeneratedParams{
			OrgID: orgID, SiteID: p.SiteID, Title: "Credit note " + no, FileName: "credit-note-" + no + ".pdf", Content: bytes.NewReader(b),
			UnitID: p.UnitID, SystemKey: "money", FolderName: "Money", FolderVisibility: documents.Visibility{Level: documents.LevelBoard},
			GeneratedType: "payment_credit_note", GeneratedID: paymentID, RecipientPersonID: p.PersonID,
		})
		if err != nil {
			return err
		}
		out = files.File{ID: d.FileID}
		_, err = tx.Exec(ctx, `UPDATE payment SET credit_note_document_id=$3 WHERE org_id=$1 AND id=$2`, orgID, paymentID, d.ID)
		return err
	})
	return out, err
}
