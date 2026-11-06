// Package statement builds account statements (hesap ekstresi) from ledger
// lines: running balance, opening and closing balance for a date range,
// principal/fee split, per debtor side (owner/tenant) and per person
// aggregation (money.md M1 and M8, backend plan §9.5 and §9.12).
//
// It is pure and in-memory; the PDF and CSV renderers consume Statement.
//
// # Sign convention
//
// Amounts are signed from the debtor's side: positive increases the balance
// owed (charge, fee, opening debt), negative decreases it (payment, waiver).
// A negative closing balance is a credit.
//
// # Range
//
// Range.From/To are inclusive calendar dates in the given zone. Lines before
// From fold into the opening balance; lines after To are ignored, so
// Closing = Opening + sum(rows). Each line moves either principal or fee (see
// Line.Part), so Closing.Principal + Closing.Fee = Closing.Total.
//
// Statement.Check verifies these invariants and Build runs it before returning.
package statement
