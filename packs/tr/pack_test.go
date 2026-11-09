// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/sdk"
)

func TestManifestIsValid(t *testing.T) {
	m := New().Manifest()
	if m.ID != "tr" || len(m.Countries) != 1 || m.Countries[0] != "TR" {
		t.Fatalf("manifest = %+v", m)
	}
	if err := sdk.Supports(m.MinSDK); err != nil {
		t.Fatal(err)
	}
}

func TestVKN(t *testing.T) {
	for _, v := range []string{"1234567890", "0000000019"} {
		if err := ValidateTaxID(v); err != nil {
			t.Errorf("%s should be valid: %v", v, err)
		}
	}
	for v, code := range map[string]string{
		"1234567891":  CodeTaxIDChecksum, // wrong check digit
		"123456789":   CodeTaxIDFormat,
		"12345678901": CodeTaxIDChecksum, // 11 digits are a TCKN, and this is not one
		"12345 67890": CodeTaxIDFormat,
		"abcdefghij":  CodeTaxIDFormat,
		"":            CodeTaxIDFormat,
	} {
		err := ValidateTaxID(v)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code() != code {
			t.Errorf("%q: %v, want code %s", v, err, code)
		}
	}
}

func TestTCKNAsTaxNumber(t *testing.T) {
	if err := ValidateTaxID("10000000146"); err != nil { // the well known test TCKN
		t.Fatal(err)
	}
	if ValidateTaxID("10000000147") == nil || ValidateTaxID("00000000000") == nil {
		t.Fatal("a wrong TCKN must be refused")
	}
}

func TestIBAN(t *testing.T) {
	valid := []string{"TR330006100519786457841326", "tr33 0006 1005 1978 6457 8413 26"}
	for _, v := range valid {
		if err := ValidateIBAN(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for v, code := range map[string]string{
		"TR330006100519786457841327": CodeIBANChecksum,
		"DE89370400440532013000":     CodeIBANFormat,
		"TR33000610051978645784132":  CodeIBANFormat,
		"TR330006110519786457841326": CodeIBANReserved,
		"TR000000000019786457841326": CodeIBANBank,
		"TR33000610051978645784132X": CodeIBANFormat,
		"":                           CodeIBANFormat,
	} {
		err := ValidateIBAN(v)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code() != code {
			// the reserved and bank code cases also fail the checksum: the specific check wins
			t.Errorf("%q: %v, want code %s", v, err, code)
		}
	}
}

func TestPlate(t *testing.T) {
	for _, v := range []string{"34 ABC 123", "06 AB 1234", "34AB123", "35 A 1234", "81 ZZ 99" + "9", "07 YKS 45", "34-abc-12"} {
		if err := ValidatePlate(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for v, code := range map[string]string{
		"00 ABC 123":  CodePlateProvince,
		"82 ABC 123":  CodePlateProvince,
		"34 ABC 1":    CodePlateFormat,
		"34 A 123":    CodePlateFormat,
		"34 ABCD 123": CodePlateFormat,
		"34 QWX 123":  CodePlateFormat, // Q, W and X are not used on Turkish plates
		"34 AİB 123":  CodePlateFormat,
		"ABC 123":     CodePlateFormat,
		"":            CodePlateFormat,
		"34 AB 12345": CodePlateFormat,
	} {
		err := ValidatePlate(v)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code() != code {
			t.Errorf("%q: %v, want code %s", v, err, code)
		}
	}
}

func TestReferenceData(t *testing.T) {
	ctx := context.Background()
	p, _ := areas{}.List(ctx, "province", "")
	if len(p) != 81 || p[0].Code != "01" || p[0].Name != "Adana" || p[33].Name != "İstanbul" || p[80].Name != "Düzce" {
		t.Fatalf("%d provinces, first %+v", len(p), p[0])
	}
	d, _ := areas{}.List(ctx, "district", "34")
	if len(d) != 39 {
		t.Fatalf("İstanbul has 39 districts, got %d", len(d))
	}
	seen := map[string]bool{}
	for _, x := range d {
		seen[x.Name] = true
		if x.Parent != "34" || !strings.HasPrefix(x.Code, "34:") {
			t.Fatalf("district %+v", x)
		}
	}
	for _, want := range []string{"Kadıköy", "Beşiktaş", "Çatalca", "Şişli"} {
		if !seen[want] {
			t.Errorf("no district %s in İstanbul", want)
		}
	}
	all, _ := areas{}.List(ctx, "district", "")
	if len(all) != 973 {
		t.Errorf("%d districts, want 973", len(all))
	}
	codes := map[string]bool{}
	for _, x := range all {
		if codes[x.Code] {
			t.Errorf("duplicate district code %s", x.Code)
		}
		codes[x.Code] = true
	}
	if none, _ := (areas{}).List(ctx, "bank", ""); len(none) != 0 {
		t.Error("an unknown kind has no items")
	}
	if len(adminAreasJSON) > 2<<20 {
		t.Error("the data file must stay far below 2 MB")
	}
}

func TestHolidays(t *testing.T) {
	ctx := context.Background()
	h, _ := areas{}.List(ctx, "holiday", "2026")
	got := map[string]string{}
	for _, x := range h {
		got[x.Code] = x.Name
	}
	for date, want := range map[string]string{
		"2026-01-01": "Yılbaşı", "2026-04-23": "Ulusal Egemenlik ve Çocuk Bayramı", "2026-05-01": "Emek ve Dayanışma Günü",
		"2026-05-19": "Atatürk'ü Anma, Gençlik ve Spor Bayramı", "2026-07-15": "Demokrasi ve Millî Birlik Günü",
		"2026-08-30": "Zafer Bayramı", "2026-10-29": "Cumhuriyet Bayramı",
		"2026-03-20": "Ramazan Bayramı 1. gün", "2026-03-22": "Ramazan Bayramı 3. gün",
		"2026-05-27": "Kurban Bayramı 1. gün", "2026-05-30": "Kurban Bayramı 4. gün",
	} {
		if got[date] != want {
			t.Errorf("%s: %q, want %q", date, got[date], want)
		}
	}
	if got["2026-03-23"] != "" || got["2026-05-31"] != "" {
		t.Error("the holiday ends after 3 and 4 days")
	}
	if !strings.Contains(got["2026-05-26"], "yarım gün") {
		t.Errorf("the arife of Kurban Bayramı is a half day: %q", got["2026-05-26"])
	}
	for _, year := range []string{"2025", "2027"} {
		if x, _ := (areas{}).List(ctx, "holiday", year); len(x) != 17 {
			t.Errorf("%s: %d holidays", year, len(x))
		}
	}
	if x, _ := (areas{}).List(ctx, "holiday", ""); len(x) != len(Holidays()) {
		t.Error("without a parent all years are served")
	}
	if got := addDays(2027, 2, 27, 3); got != "2027-03-02" {
		t.Errorf("addDays: %s", got)
	}
}

func TestChartNamesAreLocalizationOnly(t *testing.T) {
	names := chart{}.Names("tr")
	for _, code := range []string{"1100", "1200", "4000", "4100", "5030"} {
		if names[code] == "" {
			t.Errorf("no name for %s", code)
		}
	}
	if (chart{}).Names("en") != nil || (chart{}).Names("tr-TR") == nil {
		t.Error("only Turkish names are supplied")
	}
}

func TestAddressFormat(t *testing.T) {
	fields := address{}.Fields()
	byKey := map[string]sdk.AddressField{}
	for _, f := range fields {
		byKey[f.Key] = f
		if f.Pattern != "" {
			if _, err := regexp.Compile(f.Pattern); err != nil {
				t.Errorf("%s: %v", f.Key, err)
			}
		}
	}
	if byKey["province"].Reference != "province" || byKey["district"].Reference != "district" || !byKey["province"].Required {
		t.Fatalf("%+v", byKey)
	}
	if !regexp.MustCompile(byKey["postal_code"].Pattern).MatchString("34710") || regexp.MustCompile(byKey["postal_code"].Pattern).MatchString("3471") {
		t.Error("postal code pattern")
	}
}

func TestConsentTextsExistForEveryPurposeInBothLocales(t *testing.T) {
	cs := consents()
	if len(cs) != 5 {
		t.Fatalf("%d consent texts", len(cs))
	}
	for _, c := range cs {
		for _, loc := range []string{"tr", "en"} {
			txt, ok := c.Text(loc)
			if !ok || txt.Version != ConsentVersion || !strings.HasPrefix(txt.Markdown, "# ") {
				t.Errorf("%s %s: %+v %v", c.ID(), loc, txt, ok)
			}
		}
		if _, ok := c.Text("de"); ok {
			t.Errorf("%s has no German text", c.ID())
		}
	}
	notice, _ := cs[0].Text("tr")
	for _, s := range []string{"6698", "VERBİS", "m. 11", "Borçlu listeleri"} {
		if !strings.Contains(notice.Markdown, s) {
			t.Errorf("the aydınlatma metni lacks %q", s)
		}
	}
}

func TestCatalogCoversEverythingThePackAdds(t *testing.T) {
	for _, loc := range []string{"tr", "en"} {
		l := labels{}.Labels(loc)
		for _, tp := range Templates() {
			if l[tp.ID()] == "" {
				t.Errorf("%s: no label for template %s", loc, tp.ID())
			}
		}
		for _, code := range []string{CodeTaxIDFormat, CodeTaxIDChecksum, CodeIBANFormat, CodeIBANChecksum, CodeIBANReserved, CodeIBANBank, CodePlateFormat, CodePlateProvince} {
			if l[code] == "" {
				t.Errorf("%s: no label for %s", loc, code)
			}
		}
		for _, c := range consents() {
			if l[c.ID()] == "" {
				t.Errorf("%s: no label for %s", loc, c.ID())
			}
		}
		for _, f := range (address{}).Fields() {
			if l[f.LabelKey] == "" {
				t.Errorf("%s: no label for %s", loc, f.LabelKey)
			}
		}
		for k := range l {
			if !strings.HasPrefix(k, "tr.") {
				t.Errorf("key %q is not namespaced", k)
			}
		}
	}
	if (labels{}).Labels("de") != nil {
		t.Error("no German labels")
	}
}

func TestSettingsSchemas(t *testing.T) {
	for _, scope := range []sdk.ScopeKind{sdk.ScopeOrg, sdk.ScopeSite} {
		var doc map[string]any
		if err := json.Unmarshal(settings{}.Schema(scope), &doc); err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		for name := range doc["properties"].(map[string]any) {
			if !strings.HasPrefix(name, "tr_") {
				t.Errorf("%s: property %q is not namespaced", scope, name)
			}
		}
	}
	var site map[string]any
	_ = json.Unmarshal(settings{}.Schema(sdk.ScopeSite), &site)
	kv := site["properties"].(map[string]any)["tr_kvkk_data_controller"].(map[string]any)["properties"].(map[string]any)
	for _, f := range []string{"legal_name", "address", "contact", "verbis_no"} {
		if kv[f] == nil {
			t.Errorf("the data controller lacks %s", f)
		}
	}
}

// fullData is a document with every key the templates read.
func fullData() map[string]any {
	return map[string]any{
		"site_name": "Çiçek Sitesi", "kind": "ordinary", "title": "2027 olağan toplantı", "scheduled_at": "16.01.2027 19:00", "second_call": "23.01.2027 19:00",
		"location": "Site toplantı salonu", "recipient": "Ayşe Yılmaz", "qr_token": "ABC123", "issued_by": "OpenBekci", "issued_at": "01.01.2027 10:00",
		"agenda": []any{map[string]any{"position": 1, "title": "Bütçe", "will_be_voted": true, "description": "2027 bütçesi"}},
		"rows":   []any{map[string]any{"no": "1", "name": "Ayşe Yılmaz", "units": "A-1", "mode": "kendisi", "votes": 1, "share": "2,5"}},
		"call":   "first", "quorum_reached": true, "attended": 12,
		"items": []any{map[string]any{"position": 1, "title": "Bütçe", "decision": map[string]any{"year": 2027, "seq": 3, "text": "Aidat 2.000 ₺", "outcome": "accepted", "has_votes": true, "yes": 10, "no": 1, "abstain": 1,
			"dissents": []any{map[string]any{"name": "Can", "text": "Karşıyım"}}}}},
		"decisions":  []any{map[string]any{"year": 2027, "seq": 3, "text": "Aidat 2.000 ₺", "outcome": "accepted"}},
		"owners":     []any{map[string]any{"name": "Ayşe Yılmaz", "units": "A-1", "method": "posta", "sent_at": "02.01.2027", "proof": "PTT 123"}},
		"lines":      []any{map[string]any{"desc": "Ocak aidat", "amount": "2.000,00 ₺", "date": "01.01.2027", "debit": "2.000,00 ₺", "credit": "", "balance": "2.000,00 ₺"}},
		"breakdown":  []any{map[string]any{"period": "2027-01", "principal": "2.000,00 ₺", "late_fee": "10,00 ₺"}},
		"receipt_no": "2027-0001", "date": "05.01.2027", "payer": "Ayşe Yılmaz", "unit": "A-1", "total": "2.000,00 ₺", "has_debt": true, "balance": "10,00 ₺",
		"debtor_name": "Ayşe Yılmaz", "door": "1", "side_label": "kat maliki", "as_of": "05.01.2027", "principal": "2.000,00 ₺", "late_fee": "10,00 ₺", "days": 7,
		"person": "Ayşe Yılmaz", "move_out_date": "31.12.2026",
	}
}

func TestEveryTemplateRendersSparseAndFullData(t *testing.T) {
	full := fullData()
	for _, tp := range Templates() {
		for name, in := range map[string]map[string]any{"sparse": {}, "nil": nil, "full": full} {
			doc, err := tp.Render(context.Background(), sdk.RenderRequest{Locale: "tr", Data: in})
			if err != nil || !strings.HasPrefix(doc.MIME, "text/markdown") || !strings.HasPrefix(string(doc.Body), "# ") || strings.Contains(string(doc.Body), "<nil>") || strings.Contains(string(doc.Body), "%!") {
				t.Errorf("%s/%s: %v %q", tp.ID(), name, err, doc.MIME)
			}
		}
		if tp.ID() != ID+"."+tp.Kind() || len(tp.Locales()) != 1 || tp.Locales()[0] != "tr" {
			t.Errorf("template %s: kind %s", tp.ID(), tp.Kind())
		}
	}
}

func render(t *testing.T, kind string, in map[string]any) string {
	t.Helper()
	for _, tp := range Templates() {
		if tp.Kind() == kind {
			doc, err := tp.Render(context.Background(), sdk.RenderRequest{Locale: "tr", Data: in})
			if err != nil {
				t.Fatal(err)
			}
			return string(doc.Body)
		}
	}
	t.Fatalf("no template of kind %s", kind)
	return ""
}

func TestCallLetterCarriesAgendaSecondDateAndQR(t *testing.T) {
	out := render(t, KindCallLetter, map[string]any{"site_name": "Çiçek Sitesi", "kind": "ordinary", "scheduled_at": "16.01.2027 19:00", "second_call": "23.01.2027 19:00", "location": "Salon",
		"recipient": "Ayşe Yılmaz", "qr_token": "TOK", "agenda": []any{map[string]any{"position": 1, "title": "Bütçe", "will_be_voted": true}}})
	for _, s := range []string{"KAT MALİKLERİ KURULU TOPLANTI ÇAĞRISI", "Sayın Ayşe Yılmaz", "29. maddesi", "23.01.2027 19:00", "1. Bütçe (oylama yapılacaktır)", "[qr:", "olağan"} {
		if !strings.Contains(out, s) {
			t.Errorf("the call letter lacks %q:\n%s", s, out)
		}
	}
}

func TestDecisionBookDraftIsNumberedAndNeverClaimsToBeTheBook(t *testing.T) {
	out := render(t, KindDecisionBookDraft, map[string]any{"site_name": "S", "decisions": []any{map[string]any{"year": 2027, "seq": 4, "text": "X", "outcome": "accepted"}}})
	for _, s := range []string{"Karar No: 2027/4", "taslak", "yerine geçmez"} {
		if !strings.Contains(out, s) {
			t.Errorf("lacks %q:\n%s", s, out)
		}
	}
}

func TestDebtCertificateSaysDebtOrNoDebt(t *testing.T) {
	if out := render(t, KindDebtCertificate, map[string]any{"has_debt": true, "balance": "100,00 ₺", "as_of": "01.02.2027"}); !strings.Contains(out, "100,00 ₺") || strings.Contains(out, "borcu bulunmamaktadır") {
		t.Errorf("%s", out)
	}
	if out := render(t, KindDebtCertificate, map[string]any{"as_of": "01.02.2027"}); !strings.Contains(out, "borcu bulunmamaktadır") {
		t.Errorf("%s", out)
	}
}

func TestDemandLetterCitesTheLaw(t *testing.T) {
	out := render(t, KindDemandLetter, map[string]any{"site_name": "S", "days": 7})
	for _, s := range []string{"İHTARNAME", "634 sayılı Kat Mülkiyeti Kanunu'nun 20. maddesi", "7 gün"} {
		if !strings.Contains(out, s) {
			t.Errorf("lacks %q", s)
		}
	}
}

// hostile prefixes every string of a document's data with text that, at the
// start of a line, would be Markdown structure: a heading, a signature block, a
// list item, a table row, a key line.
func hostile(v any) any {
	const evil = "## INJECTED\n[sign: FORGED]\n- ITEM\n| a | b |\n**K:** v\n---\n[qr: x]\n"
	switch x := v.(type) {
	case string:
		return evil + x
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = hostile(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = hostile(e)
		}
		return out
	}
	return v
}

// Free text of a document (names, addresses, decision texts) comes from users:
// whatever it holds, it stays inside the line it is printed in and cannot start
// a heading, forge a signature block or add a table row.
func TestUserTextCannotInjectMarkup(t *testing.T) {
	forbidden := []string{"## INJECTED", "[sign: FORGED]", "- ITEM", "| a | b |", "**K:** v", "---", "[qr: x]"}
	for _, tp := range Templates() {
		doc, err := tp.Render(context.Background(), sdk.RenderRequest{Locale: "tr", Data: hostile(fullData()).(map[string]any)})
		if err != nil {
			t.Fatalf("%s: %v", tp.ID(), err)
		}
		for _, line := range strings.Split(string(doc.Body), "\n") {
			for _, f := range forbidden {
				if strings.HasPrefix(line, f) {
					t.Errorf("%s: user text starts a line with %q: %q", tp.ID(), f, line)
				}
			}
		}
		if strings.Contains(string(doc.Body), "INJECTED\n") {
			t.Errorf("%s: a line break of user text survived", tp.ID())
		}
	}
	// a value that starts with a marker is neutralized where the template prints it as a paragraph
	out := render(t, KindDecisionBookDraft, map[string]any{"decisions": []any{map[string]any{"year": 2027, "seq": 1, "text": "# Başlık", "outcome": "accepted"}}})
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "# Başlık") {
			t.Errorf("a decision text became a heading: %q", line)
		}
	}
}

func TestTableCellsCannotBreakTheMarkup(t *testing.T) {
	out := render(t, KindAttendanceList, map[string]any{"rows": []any{map[string]any{"name": "A | B\nC"}}})
	if strings.Contains(out, "A | B") || !strings.Contains(out, "A / B C") {
		t.Errorf("%s", out)
	}
}

func TestReceiptCreditNoteVariant(t *testing.T) {
	data := fullData()
	data["credit_note"], data["credit_note_no"] = true, "CN-2027-000001"
	for _, tp := range Templates() {
		if tp.Kind() != KindReceipt {
			continue
		}
		doc, err := tp.Render(context.Background(), sdk.RenderRequest{Locale: "tr", Data: data})
		if err != nil || !strings.Contains(string(doc.Body), "İADE MAKBUZU") || !strings.Contains(string(doc.Body), "CN-2027-000001") || strings.Contains(string(doc.Body), "Toplam tahsilat") {
			t.Errorf("credit note: %v %s", err, doc.Body)
		}
	}
}
