package governance

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/skip2/go-qrcode"
)

// DocBlock is one block of a generated document.
type DocBlock struct {
	Kind   string // h (heading), p (paragraph), li (list item), kv (label: value), table, qr, sign, space
	Text   string
	Label  string
	Header []string
	Rows   [][]string
	Widths []float64 // table column weights
	PNG    []byte    // qr: the image
}

// DocModel is a generated document before it is typeset: the same model gives
// the golden text (tests) and the PDF.
type DocModel struct {
	Title string
	Lines []string // subtitle lines under the title
	Body  []DocBlock
}

// Text renders the model as plain text (golden tests, text previews).
func (m DocModel) Text() string {
	var b strings.Builder
	b.WriteString(m.Title + "\n")
	for _, l := range m.Lines {
		b.WriteString(l + "\n")
	}
	for _, blk := range m.Body {
		switch blk.Kind {
		case "h":
			b.WriteString("\n" + blk.Text + "\n")
		case "p":
			b.WriteString(blk.Text + "\n")
		case "li":
			b.WriteString("  " + blk.Text + "\n")
		case "kv":
			b.WriteString(blk.Label + ": " + blk.Text + "\n")
		case "table":
			b.WriteString(strings.Join(blk.Header, " | ") + "\n")
			for _, r := range blk.Rows {
				b.WriteString(strings.Join(r, " | ") + "\n")
			}
		case "qr":
			b.WriteString("[QR] " + blk.Text + "\n")
		case "sign":
			b.WriteString(blk.Text + ": ____________________\n")
		case "space":
			b.WriteString("\n")
		}
	}
	return b.String()
}

// PDF typesets the model (A4, an embedded Unicode font so Turkish letters print).
func (m DocModel) PDF() ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddUTF8FontFromBytes("noto", "", notoRegular)
	pdf.AddUTF8FontFromBytes("noto", "B", notoBold)
	pdf.SetTitle(m.Title, true)
	pdf.SetCreator("OpenBekci", true)
	pdf.AddPage()
	const lh = 5.5
	pdf.SetFont("noto", "B", 14)
	pdf.MultiCell(0, 7, m.Title, "", "C", false)
	pdf.SetFont("noto", "", 10)
	for _, l := range m.Lines {
		pdf.MultiCell(0, lh, l, "", "C", false)
	}
	pdf.Ln(4)
	imgN := 0
	for _, blk := range m.Body {
		pdf.SetFont("noto", "", 10.5)
		switch blk.Kind {
		case "h":
			pdf.Ln(2)
			pdf.SetFont("noto", "B", 11.5)
			pdf.MultiCell(0, 6.5, blk.Text, "", "L", false)
		case "p":
			pdf.MultiCell(0, lh, blk.Text, "", "L", false)
			pdf.Ln(1.5)
		case "li":
			pdf.SetX(26)
			pdf.MultiCell(0, lh, blk.Text, "", "L", false)
		case "kv":
			pdf.SetFont("noto", "B", 10.5)
			pdf.CellFormat(55, lh, blk.Label, "", 0, "L", false, 0, "")
			pdf.SetFont("noto", "", 10.5)
			pdf.MultiCell(0, lh, blk.Text, "", "L", false)
		case "table":
			drawTable(pdf, blk)
		case "qr":
			imgN++
			name := fmt.Sprintf("qr%d", imgN)
			pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(blk.PNG))
			if pdf.GetY() > 230 {
				pdf.AddPage()
			}
			pdf.ImageOptions(name, pdf.GetX(), pdf.GetY(), 38, 38, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
			pdf.Ln(40)
			pdf.SetFont("noto", "", 9)
			pdf.MultiCell(0, 4.5, blk.Text, "", "L", false)
		case "sign":
			pdf.Ln(12)
			pdf.CellFormat(70, lh, blk.Text, "T", 0, "C", false, 0, "")
			pdf.Ln(lh)
		case "space":
			pdf.Ln(4)
		}
	}
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("render pdf: %w", err)
	}
	return out.Bytes(), nil
}

func drawTable(pdf *fpdf.Fpdf, blk DocBlock) {
	pw, _ := pdf.GetPageSize()
	l, _, r, _ := pdf.GetMargins()
	avail := pw - l - r
	weights := blk.Widths
	if len(weights) != len(blk.Header) {
		weights = make([]float64, len(blk.Header))
		for i := range weights {
			weights[i] = 1
		}
	}
	sum := 0.0
	for _, w := range weights {
		sum += w
	}
	widths := make([]float64, len(weights))
	for i, w := range weights {
		widths[i] = avail * w / sum
	}
	header := func() {
		pdf.SetFont("noto", "B", 9)
		for i, h := range blk.Header {
			pdf.CellFormat(widths[i], 7, h, "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetFont("noto", "", 9)
	}
	header()
	// Cells wrap onto several lines; a row is as tall as its tallest cell and
	// moves to the next page whole. Nothing is cut: owner names and unit labels
	// of the hazirun cetveli must be readable in full.
	const lineH = 4.6
	_, pageH := pdf.GetPageSize()
	_, _, _, bottom := pdf.GetMargins()
	for _, row := range blk.Rows {
		lines := 1
		for i, c := range row {
			if i < len(widths) {
				if n := len(wrapLines(pdf, c, widths[i])); n > lines {
					lines = n
				}
			}
		}
		h := float64(lines)*lineH + 1.6
		if pdf.GetY()+h > pageH-bottom {
			pdf.AddPage()
			header()
		}
		x, y := l, pdf.GetY()
		for i, c := range row {
			if i >= len(widths) {
				break
			}
			pdf.Rect(x, y, widths[i], h, "D")
			pdf.SetXY(x+1, y+0.8)
			for _, ln := range wrapLines(pdf, c, widths[i]) {
				pdf.CellFormat(widths[i]-2, lineH, ln, "", 2, "L", false, 0, "")
			}
			x += widths[i]
		}
		pdf.SetXY(l, y+h)
	}
	pdf.Ln(2)
}

// wrapLines splits a cell's text into the lines that fit a column width in the
// current font; an unbreakable word is broken by the font metrics. It never
// drops text.
func wrapLines(pdf *fpdf.Fpdf, text string, width float64) []string {
	if text == "" {
		return []string{""}
	}
	out := pdf.SplitText(text, width-2)
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// QRPNG encodes payload as a QR code image.
func QRPNG(payload string) ([]byte, error) {
	return qrcode.Encode(payload, qrcode.Medium, 256)
}

// msgs are the words of the generated documents. Turkish is the default for
// Turkish sites; English is the fallback. The Turkish pack may later replace
// the templates; these are the generic ones.
type msgs struct {
	callTitle, ordinary, extraordinary, dear, dearOwner string
	callBody, secondCall, agenda, votedMark, attend     string
	qrLine, issued, titleLine                           string
	sheetTitle, sheetNo, sheetName, sheetUnits          string
	sheetMode, sheetVotes, sheetShare, sheetSign        string
	inPerson, byProxy, sheetEligible, sheetPresent      string
	sheetQuorum, yes, no, first, second, noQuorum       string
	minutesTitle, minutesIntro, minutesCall             string
	minutesQuorum, minutesAtt, minutesItems             string
	decisionNo, outcome, votes, dissent, noDecision     string
	chair, secretary, draftNote                         string
	accepted, rejected, noVote, informed, locationLabel string
	dateLabel, siteLabel, notVoted                      string
}

var trMsgs = msgs{
	callTitle: "KAT MALİKLERİ KURULU TOPLANTI ÇAĞRISI", ordinary: "olağan", extraordinary: "olağanüstü",
	dear: "Sayın %s,", dearOwner: "Kat Maliki",
	callBody:   "%s kat malikleri kurulu %s toplantısı %s tarihinde, %s adresinde yapılacaktır.",
	secondCall: "Birinci toplantıda toplantı yeter sayısı sağlanamazsa ikinci toplantı %s tarihinde, aynı yerde yapılacaktır.",
	agenda:     "Gündem", votedMark: "(oylama yapılacaktır)",
	attend: "Toplantıya kendiniz veya vekiliniz aracılığıyla katılabilirsiniz; vekâletname toplantı girişinde teslim edilir.",
	qrLine: "Toplantı girişinde okutacağınız karekod (kimlik kontrolü için yanınızda bulundurunuz). Kod: %s",
	issued: "Bu çağrı %s tarafından %s tarihinde hazırlanmıştır.", titleLine: "%s",
	sheetTitle: "HAZİRUN CETVELİ", sheetNo: "Sıra", sheetName: "Ad soyad", sheetUnits: "Bağımsız bölümler",
	sheetMode: "Katılım", sheetVotes: "Oy", sheetShare: "Arsa payı", sheetSign: "İmza",
	inPerson: "kendisi", byProxy: "vekil: %s", sheetEligible: "Toplam oy hakkı: %d, arsa payı: %s",
	sheetPresent: "Toplantıda temsil edilen oy: %d, arsa payı: %s",
	sheetQuorum:  "Toplantı yeter sayısı (%s toplantı): %s", yes: "sağlandı", no: "sağlanamadı",
	first: "birinci", second: "ikinci", noQuorum: "ikinci toplantıda yeter sayı aranmaz",
	minutesTitle: "KAT MALİKLERİ KURULU TOPLANTI TUTANAĞI", minutesIntro: "%[2]s kat malikleri kurulu %[1]s toplantısı %[3]s tarihinde %[4]s adresinde yapılmıştır.",
	minutesCall:   "Çağrı: toplantı %s için çağrılmış, ikinci toplantı tarihi %s olarak bildirilmiştir.",
	minutesQuorum: "Toplantı yeter sayısı (%s toplantı): %s. Oy hakkı %d, temsil edilen %d; arsa payı %s, temsil edilen %s.",
	minutesAtt:    "Hazirun cetveline göre %d kişi katılmıştır.", minutesItems: "Gündem maddeleri ve kararlar",
	decisionNo: "Karar No", outcome: "Sonuç", votes: "Oylar: kabul %d, ret %d, çekimser %d",
	dissent: "Muhalefet şerhi (%s): %s", noDecision: "Bu madde için karar kaydedilmemiştir.",
	chair: "Toplantı başkanı", secretary: "Yazman", locationLabel: "Yer", dateLabel: "Tarih", siteLabel: "Site",
	draftNote: "Bu belge noter onaylı karar defterine geçirilmek üzere hazırlanmış numaralı bir taslaktır; karar defterinin yerine geçmez.",
	accepted:  "kabul edildi", rejected: "reddedildi", noVote: "oylanmadı", informed: "bilgi için kaydedildi", notVoted: "oylama yapılmadı",
}

var enMsgs = msgs{
	callTitle: "OWNERS' ASSEMBLY CALL", ordinary: "ordinary", extraordinary: "extraordinary",
	dear: "Dear %s,", dearOwner: "owner",
	callBody:   "The %s owners' assembly of %s will be held on %s at %s.",
	secondCall: "If there is no quorum at the first meeting, the second meeting is held on %s at the same place.",
	agenda:     "Agenda", votedMark: "(to be voted)",
	attend: "You may attend yourself or through a proxy; hand in the proxy letter at the entrance.",
	qrLine: "Your check-in QR code for the entrance (bring an ID). Code: %s",
	issued: "Prepared by %s on %s.", titleLine: "%s",
	sheetTitle: "ATTENDANCE LIST", sheetNo: "No.", sheetName: "Name", sheetUnits: "Units",
	sheetMode: "Attendance", sheetVotes: "Votes", sheetShare: "Land share", sheetSign: "Signature",
	inPerson: "in person", byProxy: "proxy for %s", sheetEligible: "Total votes: %d, land share: %s",
	sheetPresent: "Represented in the room: %d votes, land share: %s",
	sheetQuorum:  "Quorum (%s call): %s", yes: "reached", no: "not reached",
	first: "first", second: "second", noQuorum: "no quorum is required at the second call",
	minutesTitle: "OWNERS' ASSEMBLY MINUTES", minutesIntro: "The %s owners' assembly of %s was held on %s at %s.",
	minutesCall:   "Call: the meeting was called for %s; the second meeting date was given as %s.",
	minutesQuorum: "Quorum (%s call): %s. Votes %d, represented %d; land share %s, represented %s.",
	minutesAtt:    "According to the attendance list %d people attended.", minutesItems: "Agenda items and decisions",
	decisionNo: "Decision no.", outcome: "Outcome", votes: "Votes: yes %d, no %d, abstain %d",
	dissent: "Dissent (%s): %s", noDecision: "No decision was recorded for this item.",
	chair: "Chair", secretary: "Secretary", locationLabel: "Place", dateLabel: "Date", siteLabel: "Site",
	draftNote: "This is a numbered draft for transcription into the notarized decision book; it is not the decision book.",
	accepted:  "accepted", rejected: "rejected", noVote: "no vote", informed: "noted", notVoted: "no vote taken",
}

func msgsFor(locale string) msgs {
	if strings.HasPrefix(strings.ToLower(locale), "tr") {
		return trMsgs
	}
	return enMsgs
}

func fmtTime(t time.Time, loc *time.Location, locale string) string {
	t = t.In(loc)
	if strings.HasPrefix(strings.ToLower(locale), "tr") {
		return t.Format("02.01.2006 15:04")
	}
	return t.Format("2 Jan 2006 15:04")
}

func fmtShare(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
}

// AgendaLine is an agenda item as the documents print it.
type AgendaLine struct {
	Position    int
	Title       string
	Description string
	WillBeVoted bool
}

// CallLetterData is what the call letter needs.
type CallLetterData struct {
	SiteName    string
	Kind        string
	Title       string
	Locale      string
	Loc         *time.Location
	ScheduledAt time.Time
	SecondCall  time.Time
	Location    string
	Agenda      []AgendaLine
	Recipient   string // empty: the generic letter
	QRToken     string
	QRPayload   string
	QRPNG       []byte
	Instance    string
	IssuedAt    time.Time
}

// CallLetter builds the call letter (G3): generic, or personal with the owner's check-in QR.
func CallLetter(d CallLetterData) DocModel {
	m := msgsFor(d.Locale)
	kind := m.ordinary
	if d.Kind == "extraordinary" {
		kind = m.extraordinary
	}
	title := m.callTitle
	doc := DocModel{Title: title, Lines: []string{d.SiteName}}
	if d.Title != "" {
		doc.Lines = append(doc.Lines, d.Title)
	}
	who := m.dearOwner
	if d.Recipient != "" {
		who = d.Recipient
	}
	doc.Body = append(doc.Body,
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.dear, who)},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.callBody, d.SiteName, kind, fmtTime(d.ScheduledAt, d.Loc, d.Locale), d.Location)},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.secondCall, fmtTime(d.SecondCall, d.Loc, d.Locale))},
		DocBlock{Kind: "h", Text: m.agenda},
	)
	for _, a := range d.Agenda {
		line := fmt.Sprintf("%d. %s", a.Position, a.Title)
		if a.WillBeVoted {
			line += " " + m.votedMark
		}
		doc.Body = append(doc.Body, DocBlock{Kind: "li", Text: line})
		if a.Description != "" {
			doc.Body = append(doc.Body, DocBlock{Kind: "li", Text: "   " + a.Description})
		}
	}
	doc.Body = append(doc.Body, DocBlock{Kind: "space"}, DocBlock{Kind: "p", Text: m.attend})
	if d.QRToken != "" {
		doc.Body = append(doc.Body, DocBlock{Kind: "qr", Text: fmt.Sprintf(m.qrLine, d.QRToken), PNG: d.QRPNG, Label: d.QRPayload})
	}
	doc.Body = append(doc.Body, DocBlock{Kind: "space"},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.issued, d.Instance, fmtTime(d.IssuedAt, d.Loc, d.Locale))})
	return doc
}

// SheetRow is one line of the attendance list.
type SheetRow struct {
	Name     string
	Units    []string
	ProxyFor string
	Votes    int64
	Share    float64
}

// AttendanceSheetData is what the hazirun cetveli needs.
type AttendanceSheetData struct {
	SiteName    string
	Locale      string
	Loc         *time.Location
	ScheduledAt time.Time
	Location    string
	Call        string
	Rows        []SheetRow
	Eligible    Weight
	Present     Weight
	Quorum      QuorumResult
}

// AttendanceSheet builds the hazirun cetveli (G4).
func AttendanceSheet(d AttendanceSheetData) DocModel {
	m := msgsFor(d.Locale)
	doc := DocModel{Title: m.sheetTitle, Lines: []string{d.SiteName, fmtTime(d.ScheduledAt, d.Loc, d.Locale) + ", " + d.Location}}
	t := DocBlock{Kind: "table", Header: []string{m.sheetNo, m.sheetName, m.sheetUnits, m.sheetMode, m.sheetVotes, m.sheetShare, m.sheetSign}, Widths: []float64{0.6, 2.4, 1.8, 1.8, 0.7, 1, 1.6}}
	for i, r := range d.Rows {
		mode := m.inPerson
		if r.ProxyFor != "" {
			mode = fmt.Sprintf(m.byProxy, r.ProxyFor)
		}
		t.Rows = append(t.Rows, []string{fmt.Sprint(i + 1), r.Name, strings.Join(r.Units, ", "), mode, fmt.Sprint(r.Votes), fmtShare(r.Share), ""})
	}
	doc.Body = append(doc.Body, t,
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.sheetEligible, d.Eligible.Votes, fmtShare(d.Eligible.Share))},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.sheetPresent, d.Present.Votes, fmtShare(d.Present.Share))})
	reached := m.no
	if d.Quorum.Reached {
		reached = m.yes
	}
	call := m.first
	if d.Call == callSecond {
		call = m.second
	}
	q := fmt.Sprintf(m.sheetQuorum, call, reached)
	if d.Quorum.NoQuorumSecondCall {
		q += " (" + m.noQuorum + ")"
	}
	doc.Body = append(doc.Body, DocBlock{Kind: "p", Text: q}, DocBlock{Kind: "sign", Text: m.chair}, DocBlock{Kind: "sign", Text: m.secretary})
	return doc
}

// MinutesItem is an agenda item with its decision for the minutes.
type MinutesItem struct {
	Position    int
	Title       string
	WillBeVoted bool
	Decision    *MinutesDecision
}

// MinutesDecision is a decision as the minutes print it.
type MinutesDecision struct {
	Year, Seq        int
	Text, Outcome    string
	HasVotes         bool
	Yes, No, Abstain int64
	Dissents         []MinutesDissent
}

// MinutesDissent is one muhalefet serhi.
type MinutesDissent struct{ Name, Text string }

// MinutesData is what the minutes need.
type MinutesData struct {
	SiteName    string
	Kind        string
	Locale      string
	Loc         *time.Location
	ScheduledAt time.Time
	SecondCall  time.Time
	Location    string
	Call        string
	Attended    int
	Eligible    Weight
	Present     Weight
	Quorum      QuorumResult
	Items       []MinutesItem
}

// Minutes builds the minutes (tutanak, G1): numbered decisions for transcription into the karar defteri.
func Minutes(d MinutesData) DocModel {
	m := msgsFor(d.Locale)
	kind := m.ordinary
	if d.Kind == "extraordinary" {
		kind = m.extraordinary
	}
	doc := DocModel{Title: m.minutesTitle, Lines: []string{d.SiteName}}
	call := m.first
	if d.Call == callSecond {
		call = m.second
	}
	reached := m.no
	if d.Quorum.Reached {
		reached = m.yes
	}
	doc.Body = append(doc.Body,
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.minutesIntro, kind, d.SiteName, fmtTime(d.ScheduledAt, d.Loc, d.Locale), d.Location)},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.minutesCall, fmtTime(d.ScheduledAt, d.Loc, d.Locale), fmtTime(d.SecondCall, d.Loc, d.Locale))},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.minutesQuorum, call, reached, d.Eligible.Votes, d.Present.Votes, fmtShare(d.Eligible.Share), fmtShare(d.Present.Share))},
		DocBlock{Kind: "p", Text: fmt.Sprintf(m.minutesAtt, d.Attended)},
		DocBlock{Kind: "h", Text: m.minutesItems},
	)
	outcomes := map[string]string{"accepted": m.accepted, "rejected": m.rejected, "no_vote": m.noVote, "noted": m.informed}
	for _, it := range d.Items {
		doc.Body = append(doc.Body, DocBlock{Kind: "p", Text: fmt.Sprintf("%d. %s", it.Position, it.Title)})
		if it.Decision == nil {
			doc.Body = append(doc.Body, DocBlock{Kind: "li", Text: m.noDecision})
			continue
		}
		dec := it.Decision
		out := outcomes[dec.Outcome]
		if out == "" {
			out = dec.Outcome
		}
		doc.Body = append(doc.Body,
			DocBlock{Kind: "li", Text: fmt.Sprintf("%s: %d/%d", m.decisionNo, dec.Year, dec.Seq)},
			DocBlock{Kind: "li", Text: dec.Text},
			DocBlock{Kind: "li", Text: m.outcome + ": " + out})
		if dec.HasVotes {
			doc.Body = append(doc.Body, DocBlock{Kind: "li", Text: fmt.Sprintf(m.votes, dec.Yes, dec.No, dec.Abstain)})
		}
		for _, ds := range dec.Dissents {
			doc.Body = append(doc.Body, DocBlock{Kind: "li", Text: fmt.Sprintf(m.dissent, ds.Name, ds.Text)})
		}
	}
	doc.Body = append(doc.Body, DocBlock{Kind: "space"}, DocBlock{Kind: "p", Text: m.draftNote},
		DocBlock{Kind: "sign", Text: m.chair}, DocBlock{Kind: "sign", Text: m.secretary})
	return doc
}
