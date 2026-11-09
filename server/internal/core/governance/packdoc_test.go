package governance

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
)

func TestModelFromMarkdownMapsTheSubsetToBlocks(t *testing.T) {
	md := "# BAŞLIK\n> Site\n> alt satır\n\nSayın Ayşe,\nikinci satır\n\n## Gündem\n- 1. Bütçe\n**Tarih:** 01.02.2027\n\n| A | B |\n|---|---|\n| 1 | 2 |\n| 3 |\n\n[sign: Başkan]\n[qr: kod ABC]\n---\n"
	png, _ := QRPNG("x")
	m := ModelFromMarkdown(md, png)
	if m.Title != "BAŞLIK" || len(m.Lines) != 2 || m.Lines[1] != "alt satır" {
		t.Fatalf("title %q lines %v", m.Title, m.Lines)
	}
	kinds := []string{}
	for _, b := range m.Body {
		kinds = append(kinds, b.Kind)
	}
	if got := strings.Join(kinds, ","); got != "p,h,li,kv,table,sign,qr,space" {
		t.Fatalf("blocks %s", got)
	}
	if m.Body[0].Text != "Sayın Ayşe, ikinci satır" {
		t.Errorf("a paragraph joins its lines: %q", m.Body[0].Text)
	}
	tbl := m.Body[4]
	if len(tbl.Header) != 2 || len(tbl.Rows) != 2 || tbl.Rows[1][1] != "" || tbl.Rows[0][1] != "2" {
		t.Errorf("table %+v", tbl)
	}
	if m.Body[3].Label != "Tarih" || m.Body[3].Text != "01.02.2027" || string(m.Body[6].PNG) != string(png) {
		t.Errorf("kv/qr %+v %+v", m.Body[3], m.Body[6])
	}
	if pdf, err := m.PDF(); err != nil || !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("pdf %v", err)
	}
	// without a QR image the caption stays on the page as text
	if m2 := ModelFromMarkdown("# T\n[qr: yazı]\n", nil); m2.Body[0].Kind != "p" || m2.Body[0].Text != "yazı" {
		t.Errorf("%+v", m2.Body)
	}
}

type fakeResolver struct {
	doc doctpl.Doc
	ok  bool
	err error
}

func (f fakeResolver) Render(context.Context, uuid.UUID, string, string, map[string]any) (doctpl.Doc, bool, error) {
	return f.doc, f.ok, f.err
}

// A pack's template replaces the generic document; a missing or failing one
// leaves the generic document, and a call is never blocked by a broken template.
func TestPackTemplatesReplaceGenericDocumentsOnlyWhenTheyRender(t *testing.T) {
	s := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d := CallLetterData{SiteName: "Site", Locale: "tr", Loc: time.UTC, ScheduledAt: time.Now(), SecondCall: time.Now(), Location: "Salon"}
	generic := s.callLetterModel(context.Background(), uuid.New(), d)
	if generic.Title != "KAT MALİKLERİ KURULU TOPLANTI ÇAĞRISI" {
		t.Fatalf("generic: %q", generic.Title)
	}
	s.docResolver = fakeResolver{doc: doctpl.Doc{MIME: "text/markdown; charset=utf-8", Body: []byte("# PAKET ÇAĞRI\n\nmetin\n")}, ok: true}
	if m := s.callLetterModel(context.Background(), uuid.New(), d); m.Title != "PAKET ÇAĞRI" {
		t.Fatalf("pack: %q", m.Title)
	}
	for name, r := range map[string]fakeResolver{
		"no template":  {},
		"failing":      {err: errors.New("boom"), ok: true},
		"not markdown": {doc: doctpl.Doc{MIME: "application/pdf", Body: []byte("%PDF")}, ok: true},
	} {
		s.docResolver = r
		if m := s.callLetterModel(context.Background(), uuid.New(), d); m.Title != generic.Title {
			t.Errorf("%s: %q, want the generic document", name, m.Title)
		}
	}
}

func TestDocumentDataCarriesWhatTemplatesNeed(t *testing.T) {
	d := CallLetterData{SiteName: "S", Locale: "tr", Loc: time.UTC, Agenda: []AgendaLine{{Position: 1, Title: "T", WillBeVoted: true}}, Recipient: "A", QRToken: "tok"}.data()
	agenda, _ := d["agenda"].([]any)
	if d["site_name"] != "S" || d["qr_token"] != "tok" || len(agenda) != 1 || agenda[0].(map[string]any)["will_be_voted"] != true {
		t.Fatalf("%v", d)
	}
	sheet := AttendanceSheetData{SiteName: "S", Locale: "tr", Loc: time.UTC, Rows: []SheetRow{{Name: "A", Units: []string{"1", "2"}, ProxyFor: "B", Votes: 2, Share: 1.5}}}.data()
	row := sheet["rows"].([]any)[0].(map[string]any)
	if row["units"] != "1, 2" || row["proxy_for"] != "B" || row["share"] != "1,5" && row["share"] != "1.5" {
		t.Fatalf("%v", row)
	}
}
