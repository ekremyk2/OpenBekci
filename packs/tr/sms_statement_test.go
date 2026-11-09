// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/packs/tr/trtest"
	"github.com/openbekci/openbekci/sdk"
)

func newSMS(t *testing.T, doc map[string]any) (*iletiMerkezi, *trtest.IletiMerkezi, context.Context) {
	t.Helper()
	fake := trtest.NewIletiMerkezi("im-key", "im-hash")
	t.Cleanup(fake.Close)
	p := New(WithHTTPClient(fake.Client()), WithSMSTestEndpoint("https://api.test.invalid"))
	p.svc = stubServices{set: stubSettings{"org": doc}}
	return &iletiMerkezi{p: p}, fake, sdk.WithOrg(context.Background(), tOrg)
}

func smsDoc(extra map[string]any) map[string]any {
	m := map[string]any{"tr_sms_iletimerkezi_enabled": true, "tr_sms_iletimerkezi_username": "im-key", "tr_sms_iletimerkezi_password": "im-hash", "tr_sms_iletimerkezi_sender_id": "SITEYONT"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestSMSIsSentAsOperational(t *testing.T) {
	m, fake, ctx := newSMS(t, smsDoc(nil))
	id, err := m.Send(ctx, "+90 532 000 00 01", "Aidat hatirlatmasi")
	if err != nil || id == "" {
		t.Fatalf("send: %q %v", id, err)
	}
	got := fake.Sent()
	if len(got) != 1 || got[0].Number != "905320000001" || got[0].Sender != "SITEYONT" || got[0].Text != "Aidat hatirlatmasi" {
		t.Fatalf("sent %+v", got)
	}
	if got[0].IYS != "0" {
		t.Fatalf("iys = %q: every message is operational (iys 0), never a commercial message", got[0].IYS)
	}
}

func TestSMSErrorMapping(t *testing.T) {
	m, fake, ctx := newSMS(t, smsDoc(nil))
	for code, want := range map[int]error{401: sdk.ErrCredentials, 451: sdk.ErrRejected, 452: sdk.ErrRejected, 466: sdk.ErrRejected, 402: sdk.ErrRejected} {
		fake.SetCode(code)
		if _, err := m.Send(ctx, "+905320000001", "x"); !errors.Is(err, want) {
			t.Errorf("code %d: %v, want %v", code, err, want)
		}
	}
	fake.SetCode(0)
	bad, _, ctx2 := newSMS(t, smsDoc(map[string]any{"tr_sms_iletimerkezi_password": "wrong"}))
	if _, err := bad.Send(ctx2, "+905320000001", "x"); !errors.Is(err, sdk.ErrCredentials) {
		t.Errorf("wrong credentials: %v", err)
	}
	for name, doc := range map[string]map[string]any{
		"disabled":  smsDoc(map[string]any{"tr_sms_iletimerkezi_enabled": false}),
		"no sender": smsDoc(map[string]any{"tr_sms_iletimerkezi_sender_id": ""}),
		"no key":    {"tr_sms_iletimerkezi_enabled": true},
	} {
		mm, _, c := newSMS(t, doc)
		if _, err := mm.Send(c, "+905320000001", "x"); err == nil {
			t.Errorf("%s: sent", name)
		}
	}
	if _, err := m.Send(ctx, "not a number", "x"); !errors.Is(err, sdk.ErrRejected) {
		t.Errorf("bad number: %v", err)
	}
	// a provider that is gone is unavailable, not rejected
	fake.Close()
	if _, err := m.Send(ctx, "+905320000001", "x"); !errors.Is(err, sdk.ErrUnavailable) {
		t.Errorf("closed server: %v", err)
	}
}

func TestSMSValidateSettingsCallsTheProviderOnlyWithATestEndpoint(t *testing.T) {
	m, fake, ctx := newSMS(t, nil)
	good := smsDoc(nil)
	if err := m.p.ValidateSettings(ctx, sdk.OrgScope(), good); err != nil {
		t.Fatal(err)
	}
	if fake.Balance != 1 {
		t.Fatalf("balance calls = %d, want 1 with a test endpoint", fake.Balance)
	}
	good["tr_sms_iletimerkezi_password"] = "wrong"
	if err := m.p.ValidateSettings(ctx, sdk.OrgScope(), good); !errors.Is(err, sdk.ErrCredentials) {
		t.Fatalf("wrong credentials: %v", err)
	}
	// without a test endpoint nothing is sent to the provider
	plain := New(WithHTTPClient(fake.Client()))
	before := fake.Balance
	if err := plain.ValidateSettings(ctx, sdk.OrgScope(), smsDoc(nil)); err != nil || fake.Balance != before {
		t.Fatalf("a production pack must not call the provider when settings are saved: %v (%d calls)", err, fake.Balance-before)
	}
}

func importer(t *testing.T, id string) *bankImporter {
	t.Helper()
	fs, err := loadBankFormats()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.ID == id {
			return &bankImporter{p: New(), def: f}
		}
	}
	t.Fatalf("no format %s", id)
	return nil
}

func TestBankSamplesParse(t *testing.T) {
	for _, s := range trtest.BankSamples("") {
		t.Run(s.Name, func(t *testing.T) {
			im := importer(t, s.Importer)
			head := s.Data
			if len(head) > 1024 {
				head = head[:1024]
			}
			if !im.Detect(s.Filename, head) {
				t.Fatal("not detected")
			}
			// the other bank's importer must not take the file
			other := "tr.isbank"
			if s.Importer == other {
				other = "tr.ziraat"
			}
			if importer(t, other).Detect(s.Filename, head) {
				t.Fatalf("%s also detects the file", other)
			}
			st, err := im.Parse(context.Background(), s.Filename, s.Data)
			if err != nil {
				t.Fatal(err)
			}
			if len(st.Lines) != s.WantLines || len(st.Errors) != s.WantErrors || st.Total() != s.WantTotal {
				t.Fatalf("lines %d errors %d total %d (%v)", len(st.Lines), len(st.Errors), st.Total(), st.Errors)
			}
			if st.OpeningBalance == nil || st.ClosingBalance == nil || *st.OpeningBalance+st.Total() != *st.ClosingBalance {
				t.Fatalf("balances %v %v", st.OpeningBalance, st.ClosingBalance)
			}
			for _, l := range st.Lines {
				for k, v := range l.Raw {
					if !strings.Contains(s.Text, v) {
						t.Errorf("raw %s=%q not in the file", k, v)
					}
				}
			}
		})
	}
}

func TestZiraatCSVDetails(t *testing.T) {
	s := trtest.BankSamples("tr.ziraat")[0]
	st, err := importer(t, "tr.ziraat").Parse(context.Background(), s.Filename, s.Data)
	if err != nil {
		t.Fatal(err)
	}
	l := st.Lines[0]
	if l.BookedOn != "2026-02-03" || l.Amount != 125000 || l.Counterparty != "AYŞE YILMAZ" || l.Reference != "A-12-2026-02" || l.Balance == nil || *l.Balance != 1125000 {
		t.Errorf("line 1: %+v", l)
	}
	if st.Lines[2].Amount != -342050 || st.Lines[2].Reference != "DK1003" {
		t.Errorf("line 3 (an expense keeps the bank's own reference): %+v", st.Lines[2])
	}
	if st.Lines[3].Reference != "DK1004" || st.Lines[3].Counterparty != "FATMA DEMİR" {
		t.Errorf("line 4: %+v", st.Lines[3])
	}
	if e := st.Errors[0]; e.Field != "date" || e.Row != 5 {
		t.Errorf("row error %+v", e)
	}
}

func TestIsbankCP1254(t *testing.T) {
	s := trtest.BankSamples("tr.isbank")[0]
	if bytes.Contains(s.Data, []byte("Müşteri")) {
		t.Fatal("the fixture must not be UTF-8")
	}
	st, err := importer(t, "tr.isbank").Parse(context.Background(), s.Filename, s.Data)
	if err != nil {
		t.Fatal(err)
	}
	l := st.Lines[0]
	if l.Counterparty != "ŞÜKRÜ ÇELİK" || l.BookedOn != "2026-03-10" || l.Amount != 150000 || l.Reference != "D-5-2026-03" {
		t.Errorf("line 1: %+v", l)
	}
	if st.Lines[1].Amount != -200000 {
		t.Errorf("a debit is money out: %+v", st.Lines[1])
	}
	if *st.OpeningBalance != 2075000 || *st.ClosingBalance != 2150000 {
		t.Errorf("a newest-first file: opening %d closing %d", *st.OpeningBalance, *st.ClosingBalance)
	}
}

func TestXLSXDatesAndMixedAmounts(t *testing.T) {
	z := trtest.BankSamples("tr.ziraat")[1]
	st, err := importer(t, "tr.ziraat").Parse(context.Background(), z.Filename, z.Data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lines[1].Amount != 125000 || st.Lines[2].Amount != -48025 || st.Lines[0].Reference != "A-1-2026-03" {
		t.Errorf("%+v", st.Lines)
	}
	i := trtest.BankSamples("tr.isbank")[1]
	st, err = importer(t, "tr.isbank").Parse(context.Background(), i.Filename, i.Data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lines[0].BookedOn != "2026-04-09" || st.Lines[0].Amount != 125050 || st.Lines[0].Counterparty != "CEMİLE ŞAHİN" {
		t.Errorf("%+v", st.Lines[0])
	}
}

func TestNotAStatementIsRejected(t *testing.T) {
	for _, id := range BankFormatIDs() {
		im := importer(t, id)
		if im.Detect("x.csv", trtest.NotAStatement) {
			t.Errorf("%s detects junk", id)
		}
		if _, err := im.Parse(context.Background(), "x.csv", trtest.NotAStatement); !errors.Is(err, sdk.ErrRejected) {
			t.Errorf("%s parse of junk: %v", id, err)
		}
		if _, err := im.Parse(context.Background(), "x.xlsx", []byte("PK\x03\x04 not really")); !errors.Is(err, sdk.ErrRejected) {
			t.Errorf("%s parse of a broken workbook: %v", id, err)
		}
	}
	// an xlsx is recognized by the file name only
	z := trtest.BankSamples("tr.ziraat")[1]
	if importer(t, "tr.ziraat").Detect("statement.xlsx", z.Data[:512]) {
		t.Error("an xlsx without a bank hint in its name is not detected")
	}
}

func TestParseMoney(t *testing.T) {
	for in, want := range map[string]int64{"1.250,00": 125000, "-30,5": -3050, "1250.5": 125050, "1.250": 125000, "30,00 TL": 3000, "(30,00)": -3000, "12,00-": -1200, "+7": 700, "0,05": 5, "1.234.567,89": 123456789, "42": 4200} {
		if got, err := parseMoney(in); err != nil || got != want {
			t.Errorf("%q = %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "1,234", "--5"} {
		if _, err := parseMoney(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestDecodeTextCutInsideACharacter(t *testing.T) {
	full := []byte("Açıklama;Tutar")
	if got := decodeText(full[:4]); got != "Aç" {
		t.Errorf("%q", got)
	}
	if got := decodeText([]byte{0xef, 0xbb, 0xbf, 'A'}); got != "A" {
		t.Errorf("BOM: %q", got)
	}
	if got := decodeText([]byte{'T', 0xfd, 'r', 'k'}); got != "Tırk" {
		t.Errorf("windows-1254 byte 0xFD is dotless i: %q", got)
	}
}

func TestEnabledFormatsSetting(t *testing.T) {
	s := trtest.BankSamples("tr.ziraat")[0]
	im := importer(t, "tr.ziraat")
	ctx := sdk.WithOrg(context.Background(), tOrg)
	im.p.svc = stubServices{set: stubSettings{"org": {"tr_bank_formats": []any{"tr.isbank"}}}}
	if _, err := im.Parse(ctx, s.Filename, s.Data); !errors.Is(err, sdk.ErrRejected) {
		t.Fatalf("a format outside the organization's list: %v", err)
	}
	im.p.svc = stubServices{set: stubSettings{"org": {"tr_bank_formats": []any{"tr.ziraat"}}}}
	if _, err := im.Parse(ctx, s.Filename, s.Data); err != nil {
		t.Fatal(err)
	}
	im.p.svc = stubServices{set: stubSettings{"org": {}}}
	if _, err := im.Parse(ctx, s.Filename, s.Data); err != nil {
		t.Fatalf("an empty list enables every format: %v", err)
	}
}

func TestRefCode(t *testing.T) {
	if got := RefCode("a 12", "2026-03"); got != "A-12-2026-03" {
		t.Errorf("%q", got)
	}
	if got := RefCode("Şişli 5", "2026-11"); got != "SISLI-5-2026-11" {
		t.Errorf("%q", got)
	}
	u, p, ok := ParseRefCode("AIDAT A-12-2026-03")
	if !ok || u != "AIDAT A-12" || p != "2026-03" {
		t.Errorf("%q %q %v", u, p, ok)
	}
	for _, bad := range []string{"A-12", "A-12-2026-13", "2026-03x", ""} {
		if _, _, ok := ParseRefCode(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	u, p, ok = ParseRefCode(RefCode("B-3", "2026-01"))
	if !ok || u != "B-3" || p != "2026-01" {
		t.Errorf("round trip: %q %q", u, p)
	}
}

func TestEveryFormatHasASample(t *testing.T) {
	for _, id := range BankFormatIDs() {
		if len(trtest.BankSamples(id)) == 0 {
			t.Errorf("%s has no fixture: a bank is a format file plus a fixture", id)
		}
	}
}
