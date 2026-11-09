package sdk

import (
	"context"
	"fmt"
)

// StatementImporter parses a bank statement export (CSV, XLSX, MT940, ...)
// into lines. Matching suggestions and posting stay in the core.
//
// Core call sites: the payments import module lists the importers of the
// organization's enabled packs next to the generic CSV importer
// (GET .../bank-imports/importers) and, when a statement is uploaded with an
// importer id, calls Parse. Format detection (Detect) lets the client suggest
// the importer for a file.
//
// Privacy: Line.Raw holds the source row for support and is deleted by the
// core after 90 days. Everything the core needs must be in the typed fields;
// the conformance suite checks that stripping Raw changes nothing else.
type StatementImporter interface {
	// ID is the importer id ("<packid>" or "<packid>.<name>").
	ID() string
	// Formats lists the file formats handled: "csv", "xlsx", ...
	Formats() []string
	// Detect reports whether the file looks like this importer's format; it
	// must be cheap (the head of the file is enough) and never fail.
	Detect(filename string, head []byte) bool
	// Parse reads a whole statement. Unparseable rows are reported in
	// Statement.Errors, not as an error; an error means the file is not a
	// statement of this importer at all.
	Parse(ctx context.Context, filename string, data []byte) (Statement, error)
}

// Statement is a parsed statement.
type Statement struct {
	Currency       string
	OpeningBalance *int64 // minor units
	ClosingBalance *int64
	Lines          []StatementLine
	Errors         []RowError
}

// StatementLine is one transaction of a statement; money in is positive.
type StatementLine struct {
	LineNo       int
	BookedOn     string // civil date YYYY-MM-DD
	Amount       int64  // minor units
	Counterparty string
	Description  string
	Reference    string
	// Balance is the running balance after the line, when the file has it.
	Balance *int64
	// Raw is the source row; see the privacy note on StatementImporter.
	Raw map[string]string
}

// RowError describes a row that could not be parsed.
type RowError struct {
	Row    int
	Field  string
	Reason string
}

func (e RowError) Error() string { return fmt.Sprintf("row %d: %s: %s", e.Row, e.Field, e.Reason) }

// Total returns the sum of the line amounts.
func (s Statement) Total() int64 {
	var t int64
	for _, l := range s.Lines {
		t += l.Amount
	}
	return t
}

// StripRaw returns a copy of the statement without the raw source rows.
func (s Statement) StripRaw() Statement {
	out := s
	out.Lines = make([]StatementLine, len(s.Lines))
	for i, l := range s.Lines {
		l.Raw = nil
		out.Lines[i] = l
	}
	return out
}
