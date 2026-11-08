// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

// The Turkish document templates. Each renders the small Markdown subset the
// core typesets (see sdk.Document): the pack holds the wording, the core the
// fonts and the layout. The data each template receives is documented in
// README.md ("Document data"); every key is optional, a missing one prints
// empty so a template never fails on a sparse document.

// Template kinds.
const (
	KindCallLetter        = "call_letter"
	KindAttendanceList    = "attendance_list"
	KindMinutes           = "minutes"
	KindDeliveryChecklist = "delivery_checklist"
	KindProxyForm         = "proxy_form"
	KindDecisionBookDraft = "decision_book_draft"
	KindReceipt           = "receipt"
	KindStatement         = "statement"
	KindClosingStatement  = "closing_statement"
	KindDebtCertificate   = "debt_certificate"
	KindDemandLetter      = "demand_letter"
)

type renderFunc func(d data) string

type tmpl struct {
	kind   string
	render renderFunc
}

func (t tmpl) ID() string        { return ID + "." + t.kind }
func (t tmpl) Kind() string      { return t.kind }
func (t tmpl) Locales() []string { return []string{"tr"} }

// Render implements sdk.DocumentTemplate.
func (t tmpl) Render(_ context.Context, req sdk.RenderRequest) (sdk.Document, error) {
	body := t.render(data(req.Data))
	return sdk.Document{Filename: strings.ReplaceAll(t.kind, "_", "-") + ".md", MIME: "text/markdown; charset=utf-8", Body: []byte(body)}, nil
}

// Templates returns the pack's document templates.
func Templates() []sdk.DocumentTemplate {
	all := []tmpl{
		{KindCallLetter, callLetter},
		{KindAttendanceList, attendanceList},
		{KindMinutes, minutes},
		{KindDeliveryChecklist, deliveryChecklist},
		{KindProxyForm, proxyForm},
		{KindDecisionBookDraft, decisionBookDraft},
		{KindReceipt, receipt},
		{KindStatement, statement},
		{KindClosingStatement, closingStatement},
		{KindDebtCertificate, debtCertificate},
		{KindDemandLetter, demandLetter},
	}
	out := make([]sdk.DocumentTemplate, len(all))
	for i, t := range all {
		out[i] = t
	}
	return out
}

// ---- data access

type data map[string]any

func (d data) s(key string) string {
	v, ok := d[key]
	if !ok || v == nil {
		return ""
	}
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return fmt.Sprint(int64(f))
	}
	return fmt.Sprint(v)
}

func (d data) b(key string) bool { v, _ := d[key].(bool); return v }

func (d data) rows(key string) []data {
	switch v := d[key].(type) {
	case []any:
		out := make([]data, 0, len(v))
		for _, x := range v {
			if m, ok := x.(map[string]any); ok {
				out = append(out, data(m))
			}
		}
		return out
	case []map[string]any:
		out := make([]data, 0, len(v))
		for _, m := range v {
			out = append(out, data(m))
		}
		return out
	}
	return nil
}

func (d data) sub(key string) data {
	if m, ok := d[key].(map[string]any); ok {
		return data(m)
	}
	return nil
}

func (d data) strs(key string) []string {
	var out []string
	switch v := d[key].(type) {
	case []any:
		for _, x := range v {
			out = append(out, fmt.Sprint(x))
		}
	case []string:
		out = v
	}
	return out
}

// ---- markup helpers

type doc struct{ b strings.Builder }

func (m *doc) line(s string)  { m.b.WriteString(s + "\n") }
func (m *doc) blank()         { m.b.WriteString("\n") }
func (m *doc) title(s string) { m.line("# " + s) }
func (m *doc) sub(s string) {
	if s != "" {
		m.line("> " + s)
	}
}
func (m *doc) h(s string)         { m.blank(); m.line("## " + s) }
func (m *doc) p(s string)         { m.blank(); m.line(s) }
func (m *doc) li(s string)        { m.line("- " + s) }
func (m *doc) kv(label, v string) { m.line("**" + label + ":** " + v) }
func (m *doc) sign(label string)  { m.line("[sign: " + label + "]") }
func (m *doc) String() string     { return m.b.String() }
func (m *doc) table(h []string, rows [][]string) {
	m.blank()
	m.line("| " + strings.Join(h, " | ") + " |")
	m.line("|" + strings.Repeat("---|", len(h)))
	for _, r := range rows {
		cells := make([]string, len(h))
		for i := range cells {
			if i < len(r) {
				cells[i] = cell(r[i])
			}
		}
		m.line("| " + strings.Join(cells, " | ") + " |")
	}
}

func cell(s string) string {
	return strings.TrimSpace(strings.NewReplacer("|", "/", "\n", " ").Replace(s))
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func kindWord(kind string) string {
	if kind == "extraordinary" {
		return "olağanüstü"
	}
	return "olağan"
}

func callName(call string) string {
	if call == "second" {
		return "ikinci"
	}
	return "birinci"
}

func yesNo(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}

// lineDesc is the description of a statement line from its kind code
// (opening, charge, fee, waiver, payment) and the reference the core gives.
func lineDesc(l data) string {
	if d := l.s("desc"); d != "" {
		return d
	}
	kind := map[string]string{"opening": "Açılış bakiyesi", "charge": "Tahakkuk", "fee": "Gecikme tazminatı", "waiver": "Gecikme tazminatı affı", "payment": "Tahsilat"}[l.s("kind")]
	if kind == "" {
		kind = l.s("kind")
	}
	if ref := l.s("ref"); ref != "" {
		return kind + ": " + ref
	}
	return kind
}

// receiptDesc is the "karşılığı" text of a receipt line; an advance (a payment
// beyond what was due) has no charge to name.
func receiptDesc(l data) string {
	if l.b("advance") && l.s("desc") == "" {
		return "Avans (fazla ödeme)"
	}
	return l.s("desc")
}

func methodTR(m string) string {
	switch {
	case m == "cash":
		return "Nakit"
	case m == "bank_transfer":
		return "Havale / EFT"
	case strings.HasPrefix(m, "card"):
		return "Kredi / banka kartı"
	}
	return dash(m)
}

func outcomeTR(o string) string {
	switch o {
	case "accepted":
		return "kabul edildi"
	case "rejected":
		return "reddedildi"
	case "no_vote":
		return "oylanmadı"
	case "noted":
		return "bilgi için kaydedildi"
	}
	return o
}

// ---- kat malikleri kurulu

// callLetter: the çağrı (Law 634 art. 29, verify) with the agenda and the
// second meeting date; the personal letter carries the owner's check-in QR.
func callLetter(d data) string {
	var m doc
	m.title("KAT MALİKLERİ KURULU TOPLANTI ÇAĞRISI")
	m.sub(d.s("site_name"))
	m.sub(d.s("title"))
	who := d.s("recipient")
	if who == "" {
		who = "Kat Maliki"
	}
	m.p("Sayın " + who + ",")
	m.p(fmt.Sprintf("%s kat malikleri kurulu %s toplantısı **%s** tarihinde, **%s** adresinde yapılacaktır. Bu çağrı 634 sayılı Kat Mülkiyeti Kanunu'nun 29. maddesi uyarınca yapılmaktadır.",
		d.s("site_name"), kindWord(d.s("kind")), d.s("scheduled_at"), d.s("location")))
	m.p(fmt.Sprintf("Birinci toplantıda toplantı yeter sayısı sağlanamazsa ikinci toplantı **%s** tarihinde, aynı yerde yapılacaktır; ikinci toplantıda yeter sayı aranmaz.", d.s("second_call")))
	m.h("Gündem")
	for _, a := range d.rows("agenda") {
		line := fmt.Sprintf("%s. %s", a.s("position"), a.s("title"))
		if a.b("will_be_voted") {
			line += " (oylama yapılacaktır)"
		}
		m.li(line)
		if desc := a.s("description"); desc != "" {
			m.li("   " + desc)
		}
	}
	m.blank()
	m.p("Toplantıya kendiniz veya yazılı vekâletname ile bir vekiliniz aracılığıyla katılabilirsiniz; vekâletname toplantı girişinde teslim edilir. Kat malikleri adına birden fazla maliki bulunan bağımsız bölümlerde ortak malikler tek bir temsilci aracılığıyla oy kullanır.")
	if tok := d.s("qr_token"); tok != "" {
		m.line("[qr: Toplantı girişinde okutacağınız karekod (kimlik kontrolü için yanınızda bulundurunuz). Kod: " + tok + "]")
	}
	m.blank()
	m.p(fmt.Sprintf("Bu çağrı %s tarafından %s tarihinde hazırlanmıştır.", d.s("issued_by"), d.s("issued_at")))
	m.sign("Yönetici")
	return m.String()
}

// attendanceList: the hazirun cetveli, the legal attendance list.
func attendanceList(d data) string {
	var m doc
	m.title("HAZİRUN CETVELİ")
	m.sub(d.s("site_name"))
	m.sub(d.s("scheduled_at") + ", " + d.s("location"))
	m.p(fmt.Sprintf("%s kat malikleri kurulunun %s toplantısına katılan kat malikleri ve vekilleri:", d.s("site_name"), callName(d.s("call"))))
	var rows [][]string
	for i, r := range d.rows("rows") {
		no := r.s("no")
		if no == "" {
			no = fmt.Sprint(i + 1)
		}
		mode := r.s("mode")
		switch {
		case mode != "":
		case r.s("proxy_for") != "":
			mode = "vekil: " + r.s("proxy_for")
		default:
			mode = "kendisi"
		}
		rows = append(rows, []string{no, r.s("name"), r.s("units"), mode, r.s("votes"), r.s("share"), ""})
	}
	m.table([]string{"Sıra", "Ad soyad", "Bağımsız bölümler", "Katılım", "Oy", "Arsa payı", "İmza"}, rows)
	m.blank()
	m.line(fmt.Sprintf("Toplam oy hakkı: %s, arsa payı: %s", d.s("eligible_votes"), d.s("eligible_share")))
	m.line(fmt.Sprintf("Toplantıda temsil edilen oy: %s, arsa payı: %s", d.s("present_votes"), d.s("present_share")))
	q := fmt.Sprintf("Toplantı yeter sayısı (%s toplantı): %s", callName(d.s("call")), yesNo(d.b("quorum_reached"), "sağlandı", "sağlanamadı"))
	if d.b("no_quorum_required") {
		q += " (ikinci toplantıda yeter sayı aranmaz)"
	}
	m.p(q)
	m.sign("Toplantı başkanı")
	m.sign("Yazman")
	return m.String()
}

// minutes: the toplantı tutanağı; the decisions are numbered for transcription
// into the notarized karar defteri, which the document never replaces.
func minutes(d data) string {
	var m doc
	m.title("KAT MALİKLERİ KURULU TOPLANTI TUTANAĞI")
	m.sub(d.s("site_name"))
	m.p(fmt.Sprintf("%s kat malikleri kurulu %s toplantısı %s tarihinde %s adresinde yapılmıştır.", d.s("site_name"), kindWord(d.s("kind")), d.s("scheduled_at"), d.s("location")))
	m.p(fmt.Sprintf("Çağrı: toplantı %s için çağrılmış, ikinci toplantı tarihi %s olarak bildirilmiştir.", d.s("scheduled_at"), d.s("second_call")))
	m.p(fmt.Sprintf("Toplantı yeter sayısı (%s toplantı): %s. Oy hakkı %s, temsil edilen %s; arsa payı %s, temsil edilen %s.",
		callName(d.s("call")), yesNo(d.b("quorum_reached"), "sağlandı", "sağlanamadı"), d.s("eligible_votes"), d.s("present_votes"), d.s("eligible_share"), d.s("present_share")))
	m.p(fmt.Sprintf("Hazirun cetveline göre %s kişi katılmıştır.", d.s("attended")))
	m.h("Gündem maddeleri ve kararlar")
	for _, it := range d.rows("items") {
		m.p(fmt.Sprintf("%s. %s", it.s("position"), it.s("title")))
		dec := it.sub("decision")
		if dec == nil {
			m.li("Bu madde için karar kaydedilmemiştir.")
			continue
		}
		m.li(fmt.Sprintf("Karar No: %s/%s", dec.s("year"), dec.s("seq")))
		m.li(dec.s("text"))
		m.li("Sonuç: " + outcomeTR(dec.s("outcome")))
		if dec.b("has_votes") {
			m.li(fmt.Sprintf("Oylar: kabul %s, ret %s, çekimser %s", dec.s("yes"), dec.s("no"), dec.s("abstain")))
		}
		for _, ds := range dec.rows("dissents") {
			m.li(fmt.Sprintf("Muhalefet şerhi (%s): %s", ds.s("name"), ds.s("text")))
		}
	}
	m.blank()
	m.p("Bu belge noter onaylı karar defterine geçirilmek üzere hazırlanmış numaralı bir taslaktır; karar defterinin yerine geçmez.")
	m.sign("Toplantı başkanı")
	m.sign("Yazman")
	return m.String()
}

// deliveryChecklist: the per-owner record of how the çağrı reached each owner
// (registered mail, handed over against signature, or electronic where the
// yönetim planı allows it): what a site needs if a decision is challenged.
func deliveryChecklist(d data) string {
	var m doc
	m.title("ÇAĞRI TEBLİĞ LİSTESİ")
	m.sub(d.s("site_name"))
	m.sub(d.s("scheduled_at") + ", " + d.s("location"))
	m.p("Toplantı çağrısının kat maliklerine tebliğ şekli ve tarihi. Posta ile gönderimde PTT takip numarası, elden tesliminde imza, elektronik tebligatta (yönetim planı izin veriyorsa) gönderim kaydı belge olarak saklanır.")
	m.p(fmt.Sprintf("Çağrı en az %s gün önce yapılmalıdır; ikinci toplantı tarihi: %s.", dash(d.s("min_notice_days")), dash(d.s("second_call"))))
	var rows [][]string
	for i, r := range d.rows("owners") {
		no := r.s("no")
		if no == "" {
			no = fmt.Sprint(i + 1)
		}
		rows = append(rows, []string{no, r.s("name"), r.s("units"), dash(r.s("method")), dash(r.s("sent_at")), dash(r.s("proof")), ""})
	}
	m.table([]string{"Sıra", "Kat maliki", "Bağımsız bölümler", "Tebliğ şekli", "Tarih", "Belge / takip no", "İmza"}, rows)
	m.blank()
	m.sign("Yönetici")
	return m.String()
}

// proxyForm: the vekâletname an owner signs to be represented at the meeting.
func proxyForm(d data) string {
	var m doc
	m.title("VEKÂLETNAME")
	m.sub(d.s("site_name") + " Kat Malikleri Kurulu")
	owner := d.s("owner_name")
	if owner == "" {
		owner = "........................................................"
	}
	units := d.s("units")
	if units == "" {
		units = "......................"
	}
	m.p(fmt.Sprintf("Ben, %s, %s sitesinde %s numaralı bağımsız bölümün maliki olarak, %s tarihinde saat ...... de, %s adresinde yapılacak %s kat malikleri kurulu toplantısında ve gerekirse ikinci toplantıda beni temsil etmek, gündemdeki konularda görüş bildirmek ve oy kullanmak üzere",
		owner, d.s("site_name"), units, dash(d.s("scheduled_at")), dash(d.s("location")), kindWord(d.s("kind"))))
	m.p("Sayın ........................................................ 'yı vekil tayin ettim.")
	m.p("Vekil, 634 sayılı Kat Mülkiyeti Kanunu uyarınca yalnızca kat malikleri adına temsil yetkisine sahip olup, tek başına temsil edebileceği oy sayısına yönetim planı ve kanundaki sınırlar uygulanır. Bu vekâletname yalnızca belirtilen toplantı için geçerlidir.")
	m.blank()
	m.kv("Tarih", ".....  /  .....  /  ..........")
	m.sign("Kat maliki (ad soyad, imza)")
	m.sign("Vekil (ad soyad, imza)")
	return m.String()
}

// decisionBookDraft: a numbered draft page for transcription into the
// notarized karar defteri. The system never claims to be the decision book.
func decisionBookDraft(d data) string {
	var m doc
	m.title("KARAR DEFTERİ SAYFA TASLAĞI")
	m.sub(d.s("site_name"))
	m.sub("Noter onaylı karar defterine aktarılmak üzere taslaktır; karar defterinin yerine geçmez.")
	m.p(fmt.Sprintf("Toplantı tarihi: %s. Yer: %s.", dash(d.s("scheduled_at")), dash(d.s("location"))))
	any := false
	for _, dec := range d.rows("decisions") {
		any = true
		m.h(fmt.Sprintf("Karar No: %s/%s", dec.s("year"), dec.s("seq")))
		m.p(dec.s("text"))
		m.li("Sonuç: " + outcomeTR(dec.s("outcome")))
		if dec.b("has_votes") {
			m.li(fmt.Sprintf("Oylar: kabul %s, ret %s, çekimser %s", dec.s("yes"), dec.s("no"), dec.s("abstain")))
		}
		for _, ds := range dec.rows("dissents") {
			m.li(fmt.Sprintf("Muhalefet şerhi (%s): %s", ds.s("name"), ds.s("text")))
		}
	}
	if !any {
		m.p("Bu toplantıda kayda geçen karar yoktur.")
	}
	m.blank()
	m.p("Aktarım: yukarıdaki kararlar karar defterine yazıldıktan sonra kat maliklerince imzalanır. Defterin yıllık kapanış onayı noterde yaptırılır.")
	m.sign("Toplantı başkanı")
	m.sign("Yazman")
	return m.String()
}

// ---- aidat ve hesap

// receipt: the aidat makbuzu.
func receipt(d data) string {
	var m doc
	m.title("AİDAT MAKBUZU")
	m.sub(d.s("site_name"))
	m.kv("Makbuz no", d.s("receipt_no"))
	m.kv("Tarih", d.s("date"))
	m.kv("Ödeyen", dash(d.s("payer")))
	m.kv("Bağımsız bölüm", dash(d.s("unit")))
	m.kv("Ödeme şekli", methodTR(d.s("method")))
	var rows [][]string
	for _, l := range d.rows("lines") {
		rows = append(rows, []string{receiptDesc(l), l.s("amount")})
	}
	if len(rows) > 0 {
		m.table([]string{"Karşılığı", "Tutar"}, rows)
	}
	m.blank()
	m.kv("Toplam tahsilat", d.s("total"))
	if b := d.s("balance"); b != "" {
		m.kv("Kalan borç", b)
	}
	if c := d.s("collector"); c != "" {
		m.kv("Tahsil eden", c)
	}
	m.sign("Tahsil eden")
	return m.String()
}

// statement: the hesap ekstresi.
func statement(d data) string {
	var m doc
	m.title("HESAP EKSTRESİ")
	m.sub(d.s("site_name"))
	m.kv("Hesap sahibi", dash(d.s("debtor_name")))
	m.kv("Bağımsız bölüm", dash(d.s("unit")))
	m.kv("Dönem", dash(d.s("period_from"))+" - "+dash(d.s("as_of")))
	var rows [][]string
	for _, l := range d.rows("lines") {
		rows = append(rows, []string{l.s("date"), lineDesc(l), l.s("debit"), l.s("credit"), l.s("balance")})
	}
	m.table([]string{"Tarih", "Açıklama", "Borç", "Alacak", "Bakiye"}, rows)
	m.blank()
	if v := d.s("principal"); v != "" {
		m.kv("Anapara", v)
	}
	if v := d.s("late_fee"); v != "" {
		m.kv("Gecikme tazminatı", v)
	}
	m.kv("Toplam borç", dash(d.s("balance")))
	return m.String()
}

// closingStatement: the closing statement for a person who moves out or sells
// (decision S7): the remaining debt stays with the leaving person.
func closingStatement(d data) string {
	var m doc
	m.title("KAPANIŞ HESAP ÖZETİ")
	m.sub(d.s("site_name"))
	m.kv("Ad soyad", dash(d.s("person")))
	m.kv("Bağımsız bölüm", dash(d.s("unit")))
	m.kv("Taşınma / devir tarihi", dash(d.s("move_out_date")))
	var rows [][]string
	for _, l := range d.rows("lines") {
		rows = append(rows, []string{l.s("date"), lineDesc(l), l.s("debit"), l.s("credit"), l.s("balance")})
	}
	if len(rows) > 0 {
		m.table([]string{"Tarih", "Açıklama", "Borç", "Alacak", "Bakiye"}, rows)
	}
	m.blank()
	m.kv("Kapanış bakiyesi", dash(d.s("balance")))
	m.p("Kalan borç, ayrılan kişinin kişisel hesabında takip edilmek üzere kalır; yeni kat maliki veya kiracı sıfır bakiye ile başlar. Aidat, ödeme tarihinde bağımsız bölümde bulunan kişiye tahakkuk eder; gün esasına göre bölünmez.")
	m.sign("Yönetici")
	return m.String()
}

// debtCertificate: the borç durum belgesi issued for a sale or a deposit return.
func debtCertificate(d data) string {
	var m doc
	m.title("BORÇ DURUM BELGESİ")
	m.sub(d.s("site_name"))
	m.kv("Ad soyad", dash(d.s("person")))
	m.kv("Bağımsız bölüm", dash(d.s("unit")))
	m.kv("Tarih", dash(d.s("as_of")))
	if d.b("has_debt") {
		m.p(fmt.Sprintf("Yukarıda adı geçen kişinin %s tarihi itibarıyla site yönetimine **%s** borcu bulunmaktadır (anapara %s, gecikme tazminatı %s).", dash(d.s("as_of")), d.s("balance"), dash(d.s("principal")), dash(d.s("late_fee"))))
	} else {
		m.p(fmt.Sprintf("Yukarıda adı geçen kişinin %s tarihi itibarıyla site yönetimine aidat ve ilgili giderlerden **borcu bulunmamaktadır**.", dash(d.s("as_of"))))
	}
	m.p("Bu belge, düzenlendiği tarihteki kayıtlara göre verilmiştir; sonradan tahakkuk eden borçları kapsamaz.")
	m.sign("Yönetici")
	return m.String()
}

// demandLetter: the ihtarname; late fees are claimed under art. 20 and the
// legal follow-up under art. 22 of Law 634 (verify).
func demandLetter(d data) string {
	var m doc
	m.title("İHTARNAME")
	m.sub(d.s("site_name") + " YÖNETİMİ")
	m.kv("Tarih", d.s("date"))
	m.p("Sayın " + dash(d.s("debtor_name")) + ",")
	m.p(fmt.Sprintf("%s sitesinin %s numaralı bağımsız bölümüne ilişkin %s sıfatıyla yükümlü olduğunuz aidat ve ilgili giderler %s tarihi itibarıyla ödenmemiştir. Toplam borcunuz %s tutarındadır (anapara %s, gecikme tazminatı %s). Dönemlere göre döküm aşağıda yer almaktadır.",
		d.s("site_name"), dash(d.s("door")), d.s("side_label"), d.s("as_of"), d.s("total"), d.s("principal"), d.s("late_fee")))
	var rows [][]string
	for _, r := range d.rows("breakdown") {
		rows = append(rows, []string{r.s("period"), r.s("principal"), r.s("late_fee")})
	}
	if len(rows) > 0 {
		m.table([]string{"Dönem", "Anapara", "Gecikme tazminatı"}, rows)
	}
	m.p(fmt.Sprintf("İşbu ihtarname tarafınıza ulaştığı tarihten itibaren %s gün içinde borcunuzun ödenmesini, ödenmediği takdirde 634 sayılı Kat Mülkiyeti Kanunu'nun 20. maddesi uyarınca gecikme tazminatı ve yargılama giderleriyle birlikte, aynı Kanun'un 22. maddesi ve ilgili mevzuat çerçevesinde yasal yollara başvurulacağını ihtar ederiz.", dash(d.s("days"))))
	m.p("Ödeme yaptıysanız bu yazıyı dikkate almayınız ve ödeme belgenizi yönetime iletiniz.")
	m.p("Saygılarımızla,")
	m.sign(d.s("site_name") + " Yönetimi")
	return m.String()
}
