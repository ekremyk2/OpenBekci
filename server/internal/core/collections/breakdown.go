package collections

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/pdf"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// BreakdownLine is the debt of one charge as of a date.
type BreakdownLine struct {
	Period    *string     `json:"period"`
	ChargeID  uuid.UUID   `json:"charge_id"`
	DueDate   string      `json:"due_date"`
	Principal money.Money `json:"principal"`
	LateFee   money.Money `json:"late_fee"`
}

// Breakdown is a debtor's debt per period as of a date (the ihtarname attachment).
type Breakdown struct {
	Debtor         billing.Debtor  `json:"debtor"`
	AsOf           string          `json:"as_of"`
	Lines          []BreakdownLine `json:"lines"`
	TotalPrincipal money.Money     `json:"total_principal"`
	TotalLateFee   money.Money     `json:"total_late_fee"`
}

func periodOf(period, due string) *string {
	if period != "" {
		return &period
	}
	if len(due) >= 7 {
		p := due[:7]
		return &p
	}
	return nil
}

// breakdownTx computes the breakdown inside a transaction (the caller has checked permissions).
// Unit sides: the open principal of each charge due on or before asOf, and the late fee still owed
// (fee accrued to the date, net of waivers, minus fee payments up to the date) spread over the lines in
// proportion to the fee each charge accrued. Persons (carried debt) have no accrual: their unpaid fee
// charges are lines of their own.
func (s *Service) breakdownTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, d billing.Debtor, asOf string) (Breakdown, error) {
	cur := ledger.Currency(site)
	b := Breakdown{Debtor: d, AsOf: asOf, Lines: []BreakdownLine{}, TotalPrincipal: money.Zero(cur), TotalLateFee: money.Zero(cur)}
	charges, err := billing.LoadCharges(ctx, tx, orgID, site.ID, d)
	if err != nil {
		return b, err
	}
	periods := map[uuid.UUID]string{}
	where, args := "c.unit_id = $3 AND c.debtor_kind = $4", []any{}
	if d.IsPerson() {
		where, args = "c.debtor_kind = 'person' AND c.debtor_person_id = $3", append(args, *d.PersonID)
	} else {
		args = append(args, *d.UnitID, d.Kind)
	}
	rows, err := tx.Query(ctx, `SELECT c.id, c.period FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND `+where, append([]any{orgID, site.ID}, args...)...)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var cid uuid.UUID
		var p string
		if err := rows.Scan(&cid, &p); err != nil {
			rows.Close()
			return b, err
		}
		periods[cid] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return b, err
	}
	paidTo := func(c billing.ChargeRow) int64 {
		var n int64
		for _, a := range c.Allocs {
			if a.On <= asOf {
				n += a.Amount
			}
		}
		return n
	}
	var feePaid int64
	var weights []int64
	for _, c := range charges {
		if c.Cancelled || c.DueDate > asOf {
			continue
		}
		rem := c.Amount - paidTo(c)
		switch {
		case c.Kind == "fee" && c.Amount > 0:
			feePaid += paidTo(c)
			if d.IsPerson() && rem > 0 {
				b.Lines = append(b.Lines, BreakdownLine{Period: periodOf(periods[c.ID], c.DueDate), ChargeID: c.ID, DueDate: c.DueDate, Principal: money.Zero(cur), LateFee: money.New(rem, cur)})
				weights = append(weights, 0)
			}
		case c.Kind == "principal" && rem > 0:
			b.Lines = append(b.Lines, BreakdownLine{Period: periodOf(periods[c.ID], c.DueDate), ChargeID: c.ID, DueDate: c.DueDate, Principal: money.New(rem, cur), LateFee: money.Zero(cur)})
			weights = append(weights, 0)
		}
	}
	if !d.IsPerson() {
		acc, err := billing.AccrueTx(ctx, tx, orgID, site, d, asOf)
		if err != nil {
			return b, err
		}
		owed := max(acc.Expected.Amount-feePaid, 0)
		perCharge := map[uuid.UUID]int64{}
		var total int64
		for _, pc := range acc.PerCharge {
			perCharge[pc.ChargeID] = pc.Fee.Amount
			total += pc.Fee.Amount
		}
		for i := range b.Lines {
			weights[i] = perCharge[b.Lines[i].ChargeID]
		}
		spread(b.Lines, weights, total, owed, cur)
	}
	for _, l := range b.Lines {
		b.TotalPrincipal.Amount += l.Principal.Amount
		b.TotalLateFee.Amount += l.LateFee.Amount
	}
	return b, nil
}

// spread distributes owed over lines in proportion to weights (the remainder lands on the heaviest line);
// without weights it goes to the first line.
func spread(lines []BreakdownLine, weights []int64, total, owed int64, cur money.Code) {
	if owed == 0 || len(lines) == 0 {
		return
	}
	if total <= 0 {
		lines[0].LateFee = money.New(owed, cur)
		return
	}
	var given int64
	heavy := 0
	for i, w := range weights {
		share := owed * w / total
		lines[i].LateFee = money.New(share, cur)
		given += share
		if w > weights[heavy] {
			heavy = i
		}
	}
	lines[heavy].LateFee.Amount += owed - given
}

// Breakdown returns the debt breakdown of a debtor (collections.manage); asOf "" is today.
func (s *Service) Breakdown(ctx context.Context, orgID, siteID uuid.UUID, d billing.Debtor, asOf string) (Breakdown, error) {
	if err := d.Validate(); err != nil {
		return Breakdown{}, err
	}
	var out Breakdown
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermCollectionsManage, orgID, siteID)
		if err != nil {
			return err
		}
		if asOf == "" {
			asOf = ledger.Today(site.Timezone, s.Now())
		} else if _, err := ledger.ParseDate(asOf); err != nil {
			return bad("as_of", "invalid_format")
		}
		if err := s.checkDebtor(ctx, tx, orgID, siteID, d); err != nil {
			return err
		}
		out, err = s.breakdownTx(ctx, tx, orgID, site, d, asOf)
		return err
	})
	return out, err
}

func (s *Service) checkDebtor(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d billing.Debtor) error {
	var n int
	var err error
	if d.IsPerson() {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM person WHERE org_id=$1 AND id=$2`, orgID, *d.PersonID).Scan(&n)
	} else {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM unit WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, siteID, *d.UnitID).Scan(&n)
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return problem.NotFound("no such debtor")
	}
	return nil
}

func decimal(m money.Money) string {
	a := m.Amount
	sign := ""
	if a < 0 {
		sign, a = "-", -a
	}
	return fmt.Sprintf("%s%d.%02d", sign, a/100, a%100)
}

// FormatTL renders minor units the Turkish way: 1.234,50 followed by ₺.
func FormatTL(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	whole := fmt.Sprint(minor / 100)
	var parts []string
	for len(whole) > 3 {
		parts = append([]string{whole[len(whole)-3:]}, parts...)
		whole = whole[:len(whole)-3]
	}
	parts = append([]string{whole}, parts...)
	return fmt.Sprintf("%s%s,%02d ₺", sign, strings.Join(parts, "."), minor%100)
}

func (b Breakdown) rows() [][]string {
	rows := [][]string{{"period", "due_date", "principal", "late_fee", "total"}}
	for _, l := range b.Lines {
		p := ""
		if l.Period != nil {
			p = *l.Period
		}
		rows = append(rows, []string{p, l.DueDate, decimal(l.Principal), decimal(l.LateFee), decimal(money.New(l.Principal.Amount+l.LateFee.Amount, l.Principal.Currency))})
	}
	rows = append(rows, []string{"total", b.AsOf, decimal(b.TotalPrincipal), decimal(b.TotalLateFee), decimal(money.New(b.TotalPrincipal.Amount+b.TotalLateFee.Amount, b.TotalPrincipal.Currency))})
	return rows
}

// CSV renders the breakdown as CSV.
func (b Breakdown) CSV() ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(b.rows()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// XLSX renders the breakdown as a spreadsheet with numeric amount cells.
func (b Breakdown) XLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	const sheet = "Sheet1"
	if err := f.SetSheetName(sheet, "Debt breakdown"); err != nil {
		return nil, err
	}
	name := "Debt breakdown"
	for i, r := range b.rows() {
		row := make([]any, len(r))
		for j, c := range r {
			row[j] = c
			if i > 0 && j >= 2 {
				var a int64
				_, _ = fmt.Sscanf(strings.ReplaceAll(c, ".", ""), "%d", &a)
				row[j] = float64(a) / 100
			}
		}
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(name, cell, &row); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DocLang maps a locale ("tr", "tr-TR", "en-GB") to the language of the core's
// documents: "tr" for Turkish, "en" for everything else.
func DocLang(locale string) string {
	l := strings.ToLower(locale)
	if l == "tr" || strings.HasPrefix(l, "tr-") || strings.HasPrefix(l, "tr_") {
		return "tr"
	}
	return "en"
}

// FormatAmount renders minor units of a currency for a document in a language
// ("tr" or "en", see DocLang). The Turkish lira in Turkish keeps the form of
// FormatTL (1.234,50 ₺); other combinations use the number format of the
// language with the sign of the currency (en: $1,234.50; tr: €1.234,50).
func FormatAmount(minor int64, cur money.Code, lang string) string {
	if lang == "tr" && (cur == money.TRY || cur == "") {
		return FormatTL(minor)
	}
	tag := "en-US"
	if lang == "tr" {
		tag = "tr-TR"
	}
	m := money.New(minor, cur)
	if s, err := m.Format(tag); err == nil {
		return s
	}
	return m.String()
}

// tableLabels are the words of the breakdown table.
type tableLabels struct{ title, date, period, due, principal, late, total string }

func labelsFor(lang string) tableLabels {
	if lang == "tr" {
		return tableLabels{"Borç dökümü", "Tarih", "Dönem", "Vade", "Anapara", "Gecikme", "Toplam"}
	}
	return tableLabels{"Debt breakdown", "Date", "Period", "Due", "Principal", "Late fee", "Total"}
}

// PDF renders the breakdown as a table with the given title, in Turkish.
func (b Breakdown) PDF(title string) ([]byte, error) { return b.pdfIn(title, "tr") }

// PDFFor renders the breakdown as a table in the language of a locale (see DocLang).
func (b Breakdown) PDFFor(locale string) ([]byte, error) {
	lang := DocLang(locale)
	return b.pdfIn(labelsFor(lang).title, lang)
}

func (b Breakdown) pdfIn(title, lang string) ([]byte, error) {
	d, err := pdf.NewUnicode(title)
	if err != nil {
		return nil, err
	}
	b.drawTable(d, title, lang)
	return d.Bytes(), nil
}

func (b Breakdown) drawTable(d *pdf.UDoc, title, lang string) {
	lab := labelsFor(lang)
	fmtAmt := func(minor int64) string { return FormatAmount(minor, b.TotalPrincipal.Currency, lang) }
	d.Line(title, 14, true)
	d.Line(fmt.Sprintf("%s: %s", lab.date, b.AsOf), 10, false)
	d.Advance(6)
	x := []float64{pdf.MarginX, 130, 290, 410, 530}
	head := []string{lab.period, lab.due, lab.principal, lab.late, lab.total}
	for i, h := range head {
		if i < 2 {
			d.Text(x[i], h, 10, true)
		} else {
			d.Right(x[i], h, 10, true)
		}
	}
	d.Advance(15)
	row := func(period, due string, pr, fee int64, bold bool) {
		d.Text(x[0], period, 10, bold)
		d.Text(x[1], due, 10, bold)
		d.Right(x[2], fmtAmt(pr), 10, bold)
		d.Right(x[3], fmtAmt(fee), 10, bold)
		d.Right(x[4], fmtAmt(pr+fee), 10, bold)
		d.Advance(14)
	}
	for _, l := range b.Lines {
		p := ""
		if l.Period != nil {
			p = *l.Period
		}
		row(p, l.DueDate, l.Principal.Amount, l.LateFee.Amount, false)
	}
	row(lab.total, "", b.TotalPrincipal.Amount, b.TotalLateFee.Amount, true)
}
