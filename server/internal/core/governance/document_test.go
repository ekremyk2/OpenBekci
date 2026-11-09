package governance

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// golden compares text with testdata/<name>; UPDATE_GOLDEN=1 rewrites the file.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file:\n--- got\n%s\n--- want\n%s", name, got, string(want))
	}
}

func istanbul(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func callLetterData(t *testing.T, locale string) CallLetterData {
	loc := istanbul(t)
	return CallLetterData{SiteName: "Gül Sitesi", Kind: "general_assembly", Title: "2026 olağan genel kurul", Locale: locale, Loc: loc,
		ScheduledAt: time.Date(2026, 1, 18, 10, 0, 0, 0, loc), SecondCall: time.Date(2026, 1, 28, 10, 0, 0, 0, loc), Location: "Site toplantı salonu",
		Agenda: []AgendaLine{
			{Position: 1, Title: "Açılış ve başkanlık divanı seçimi"},
			{Position: 2, Title: "2026 bütçesinin onaylanması", Description: "Aidatın 2.000 TL olarak belirlenmesi", WillBeVoted: true},
			{Position: 3, Title: "Yönetici ve denetçi seçimi", WillBeVoted: true},
		},
		Instance: "OpenBekci", IssuedAt: time.Date(2025, 12, 20, 9, 0, 0, 0, loc)}
}

func TestCallLetterGolden(t *testing.T) {
	golden(t, "call_letter_tr.golden", CallLetter(callLetterData(t, "tr")).Text())
	d := callLetterData(t, "en")
	d.SiteName, d.Title = "Rose Court", "2026 ordinary assembly"
	golden(t, "call_letter_en.golden", CallLetter(d).Text())
}

func TestPersonalCallLetterGolden(t *testing.T) {
	d := callLetterData(t, "tr")
	d.Recipient, d.QRToken, d.QRPayload = "Ayşe Yılmaz", "abcd2345efgh6789", "https://bekci.example.org/checkin/abcd2345efgh6789"
	golden(t, "call_letter_personal_tr.golden", CallLetter(d).Text())
	png, err := QRPNG(d.QRPayload)
	if err != nil {
		t.Fatal(err)
	}
	d.QRPNG = png
	pdf, err := CallLetter(d).PDF()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 2000 {
		t.Errorf("not a plausible PDF (%d bytes)", len(pdf))
	}
}

func TestAttendanceSheetGolden(t *testing.T) {
	loc := istanbul(t)
	d := AttendanceSheetData{SiteName: "Gül Sitesi", Locale: "tr", Loc: loc, ScheduledAt: time.Date(2026, 1, 18, 10, 0, 0, 0, loc), Location: "Site toplantı salonu", Call: callFirst,
		Rows: []SheetRow{
			{Name: "Ayşe Yılmaz", Units: []string{"A-1"}, Votes: 1, Share: 12},
			{Name: "Mehmet Demir", Units: []string{"A-2", "A-3"}, Votes: 2, Share: 24},
			{Name: "Zeynep Kaya", Units: []string{"B-4"}, ProxyFor: "Can Öz", Votes: 1, Share: 11.5},
		},
		Eligible: Weight{Votes: 6, Share: 60}, Present: Weight{Votes: 4, Share: 47.5}}
	d.Quorum = EvalQuorum(law634().Quorum, callFirst, d.Eligible, d.Present)
	golden(t, "attendance_sheet_tr.golden", AttendanceSheet(d).Text())
	pdf, err := AttendanceSheet(d).PDF()
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("attendance sheet PDF: %v", err)
	}
}

func TestMinutesGolden(t *testing.T) {
	loc := istanbul(t)
	d := MinutesData{SiteName: "Gül Sitesi", Kind: "general_assembly", Locale: "tr", Loc: loc, ScheduledAt: time.Date(2026, 1, 18, 10, 0, 0, 0, loc),
		SecondCall: time.Date(2026, 1, 28, 10, 0, 0, 0, loc), Location: "Site toplantı salonu", Call: callFirst, Attended: 3,
		Eligible: Weight{Votes: 6, Share: 60}, Present: Weight{Votes: 4, Share: 47.5},
		Items: []MinutesItem{
			{Position: 1, Title: "Açılış ve başkanlık divanı seçimi"},
			{Position: 2, Title: "2026 bütçesinin onaylanması", WillBeVoted: true, Decision: &MinutesDecision{Year: 2026, Seq: 1, Text: "2026 bütçesi ve 2.000 TL aidat kabul edildi.", Outcome: "accepted",
				HasVotes: true, Yes: 3, No: 1, Dissents: []MinutesDissent{{Name: "Zeynep Kaya", Text: "Aidat artışına karşıyım."}}}},
			{Position: 3, Title: "Yönetici seçimi", WillBeVoted: true, Decision: &MinutesDecision{Year: 2026, Seq: 2, Text: "Ayşe Yılmaz bir yıl için yönetici seçildi.", Outcome: "accepted", HasVotes: true, Yes: 4}},
		}}
	d.Quorum = EvalQuorum(law634().Quorum, callFirst, d.Eligible, d.Present)
	golden(t, "minutes_tr.golden", Minutes(d).Text())
	en := d
	en.Locale, en.SiteName = "en", "Rose Court"
	golden(t, "minutes_en.golden", Minutes(en).Text())
	pdf, err := Minutes(d).PDF()
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("minutes PDF: %v", err)
	}
}
